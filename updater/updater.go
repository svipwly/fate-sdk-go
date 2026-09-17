package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	upgradeMu   sync.Mutex
	isUpgrading bool
)

// PlatformDetail contains release artifact metadata for a specific platform.
type PlatformDetail struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}

// ReleaseManifest defines the schema for the latest.json CDN manifest.
type ReleaseManifest struct {
	Name      string                    `json:"name"`
	Version   string                    `json:"version"`
	Platforms map[string]PlatformDetail `json:"platforms"`
}

// SystemVersionInfo represents runtime version details and upgrade availability.
type SystemVersionInfo struct {
	AppName       string `json:"app_name"`
	CurrentVersion string `json:"current_version"`
	LatestVersion string `json:"latest_version"`
	CanUpdate     bool   `json:"can_update"`
	DownloadURL   string `json:"download_url,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func bustCache(rawURL string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%s_t=%d", rawURL, sep, time.Now().UnixMilli())
}

type progressWriter struct {
	total      int64
	downloaded int64
	lastUpdate time.Time
	prefix     string
	isTTY      bool
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)
	now := time.Now()
	if pw.isTTY && pw.total > 0 && (now.Sub(pw.lastUpdate) >= 60*time.Millisecond || pw.downloaded == pw.total) {
		pw.lastUpdate = now
		pct := int(float64(pw.downloaded) / float64(pw.total) * 100)
		if pct > 100 {
			pct = 100
		}
		fmt.Printf("\r%s %d%%", pw.prefix, pct)
	}
	return n, nil
}

// CheckUpdate queries remote latest.json and determines if an update is available.
func CheckUpdate(manifestURL, currentVersion string) (*SystemVersionInfo, error) {
	platformKey := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	info := &SystemVersionInfo{
		CurrentVersion: currentVersion,
	}

	httpClient := &http.Client{Timeout: 8 * time.Second}
	resp, err := httpClient.Get(bustCache(manifestURL))
	if err != nil {
		return info, fmt.Errorf("fetch manifest failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return info, fmt.Errorf("manifest server returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return info, fmt.Errorf("read manifest body failed: %w", err)
	}

	var manifest ReleaseManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return info, fmt.Errorf("parse manifest JSON failed: %w", err)
	}

	info.AppName = manifest.Name
	info.LatestVersion = manifest.Version

	if entry, ok := manifest.Platforms[platformKey]; ok {
		info.DownloadURL = entry.URL
		info.SHA256 = entry.SHA256
	}

	curClean := strings.TrimPrefix(strings.ToLower(currentVersion), "v")
	latestClean := strings.TrimPrefix(strings.ToLower(manifest.Version), "v")

	if latestClean != "" && curClean != latestClean && currentVersion != "dev" {
		if CompareSemVer(manifest.Version, currentVersion) > 0 {
			info.CanUpdate = true
		}
	}

	return info, nil
}

// PrintCheckUpdate checks for remote updates and prints a clean status message.
func PrintCheckUpdate(appName, manifestURL, currentVersion string) error {
	info, err := CheckUpdate(manifestURL, currentVersion)
	if err != nil {
		return fmt.Errorf("check update failed: %w", err)
	}

	cmdName := strings.ToLower(appName)
	if info.CanUpdate {
		fmt.Printf("%s update available: %s -> %s\nRun '%s upgrade' to update.\n", cmdName, currentVersion, info.LatestVersion, cmdName)
	} else {
		fmt.Printf("✓ %s is up to date (%s)\n", cmdName, currentVersion)
	}
	return nil
}

// ExecuteSelfUpgrade downloads the new binary, verifies SHA256 checksum, and performs atomic replacement.
func ExecuteSelfUpgrade(manifestURL, currentVersion string, force bool) error {
	upgradeMu.Lock()
	if isUpgrading {
		upgradeMu.Unlock()
		return fmt.Errorf("upgrade already in progress")
	}
	isUpgrading = true
	upgradeMu.Unlock()

	defer func() {
		upgradeMu.Lock()
		isUpgrading = false
		upgradeMu.Unlock()
	}()

	info, err := CheckUpdate(manifestURL, currentVersion)
	if err != nil {
		return fmt.Errorf("check update failed: %w", err)
	}

	if !info.CanUpdate && !force {
		fmt.Printf("✓ %s is already up to date (%s)\n", strings.ToLower(info.AppName), currentVersion)
		return nil
	}

	if info.DownloadURL == "" {
		return fmt.Errorf("no release binary found for current platform (%s-%s)", runtime.GOOS, runtime.GOARCH)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determine executable path failed: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks failed: %w", err)
	}

	appName := info.AppName
	if appName == "" {
		appName = filepath.Base(execPath)
	}

	// Line 1: Found new release / Reinstalling
	if currentVersion != "" && info.LatestVersion != "" && currentVersion != info.LatestVersion {
		fmt.Printf("Found new release: %s %s -> %s\n", strings.ToLower(appName), currentVersion, info.LatestVersion)
	} else {
		fmt.Printf("Reinstalling %s %s...\n", strings.ToLower(appName), info.LatestVersion)
	}

	tmpFile := execPath + ".upgrade.tmp"
	defer os.Remove(tmpFile)

	httpClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := httpClient.Get(bustCache(info.DownloadURL))
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download server returned status %d", resp.StatusCode)
	}

	out, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("create temporary binary failed: %w", err)
	}

	// Line 2: Dynamic download progress
	prefix := "Downloading binary..."
	if resp.ContentLength > 0 {
		sizeMB := float64(resp.ContentLength) / (1024 * 1024)
		prefix = fmt.Sprintf("Downloading binary (%.1f MB)...", sizeMB)
	}
	fmt.Print(prefix)

	pw := &progressWriter{
		total:      resp.ContentLength,
		prefix:     prefix,
		isTTY:      isTerminal(),
		lastUpdate: time.Now(),
	}

	hasher := sha256.New()
	multiWriter := io.MultiWriter(out, hasher, pw)

	if _, err := io.Copy(multiWriter, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("save stream failed: %w", err)
	}
	_ = out.Sync()
	out.Close()

	if pw.isTTY {
		fmt.Printf("\r%s done\n", prefix)
	} else {
		fmt.Println(" done")
	}

	// Line 3: Verifying and installing...
	fmt.Print("Verifying and installing... ")

	if info.SHA256 != "" {
		computedSHA := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(computedSHA, info.SHA256) {
			fmt.Println("failed")
			return fmt.Errorf("checksum mismatch: expected %s, got %s", info.SHA256, computedSHA)
		}
	}

	_ = os.Chmod(tmpFile, 0755)
	if err := os.Rename(tmpFile, execPath); err != nil {
		fmt.Println("failed")
		return fmt.Errorf("replace binary failed: %w", err)
	}
	_ = os.Chmod(execPath, 0755)
	fmt.Println("done")

	// Line 4: Success confirmation
	fmt.Printf("✓ Successfully upgraded to %s\n", info.LatestVersion)

	// Smart auto-restart if running as an active systemd service
	svcName := strings.ToLower(appName)
	restartSystemdServiceIfActive(svcName)

	return nil
}

// restartSystemdServiceIfActive checks if a systemd service exists and is active, and restarts it.
func restartSystemdServiceIfActive(serviceName string) {
	if runtime.GOOS != "linux" || serviceName == "" {
		return
	}
	systemctlPath, err := exec.LookPath("systemctl")
	if err != nil {
		return
	}

	// Check if service is active: systemctl is-active --quiet <serviceName>
	if err := exec.Command(systemctlPath, "is-active", "--quiet", serviceName).Run(); err == nil {
		fmt.Printf("Restarting systemd service (%s)...\n", serviceName)
		if err := exec.Command(systemctlPath, "restart", serviceName).Run(); err == nil {
			fmt.Printf("✓ Restarted systemd service: %s\n", serviceName)
		} else {
			fmt.Printf("! Notice: could not restart %s.service: %v\n  Please run 'systemctl restart %s' manually.\n", serviceName, err, serviceName)
		}
	}
}

// HandleUpgradeCmd checks command-line arguments for upgrade/check queries.
// Supported subcommands:
//   - "check": inspects remote release without modifying files.
//   - "upgrade": downloads, verifies, installs, and cleanly exits.
// Options:
//   - "-f", "--force": forces reinstall/redownload even if already up to date.
// Returns true if an upgrade command was handled, allowing main() to cleanly exit.
func HandleUpgradeCmd(appName, currentVersion, manifestURL string) bool {
	if len(os.Args) > 1 {
		arg := strings.ToLower(strings.TrimSpace(os.Args[1]))

		switch arg {
		case "check":
			_ = PrintCheckUpdate(appName, manifestURL, currentVersion)
			return true

		case "upgrade":
			force := false
			for _, a := range os.Args[2:] {
				if a == "-f" || a == "--force" {
					force = true
				}
			}

			if err := ExecuteSelfUpgrade(manifestURL, currentVersion, force); err != nil {
				fmt.Fprintf(os.Stderr, "error: upgrade failed: %v\n", err)
				os.Exit(1)
			}
			return true
		}
	}
	return false
}

