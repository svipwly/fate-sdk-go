package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
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
	Name         string                    `json:"name"`
	Version      string                    `json:"version"`
	PublishedAt  string                    `json:"published_at"`
	ReleaseNotes string                    `json:"release_notes,omitempty"`
	Platforms    map[string]PlatformDetail `json:"platforms"`
}

// SystemVersionInfo represents runtime version details and upgrade availability.
type SystemVersionInfo struct {
	AppName          string `json:"app_name,omitempty"`
	CurrentVersion   string `json:"current_version"`
	CurrentCommit    string `json:"current_commit"`
	CurrentBuildTime string `json:"current_build_time,omitempty"`
	CurrentOS        string `json:"current_os"`
	CurrentArch      string `json:"current_arch"`
	LatestVersion    string `json:"latest_version"`
	PublishedAt      string `json:"published_at,omitempty"`
	ReleaseNotes     string `json:"release_notes,omitempty"`
	CanUpdate        bool   `json:"can_update"`
	DownloadURL      string `json:"download_url,omitempty"`
	SHA256           string `json:"sha256,omitempty"`
	IsUpgrading      bool   `json:"is_upgrading"`
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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
func CheckUpdate(manifestURL, currentVersion, currentCommit, currentBuildTime string) (*SystemVersionInfo, error) {
	platformKey := fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)

	info := &SystemVersionInfo{
		CurrentVersion:   currentVersion,
		CurrentCommit:    currentCommit,
		CurrentBuildTime: currentBuildTime,
		CurrentOS:        runtime.GOOS,
		CurrentArch:      runtime.GOARCH,
		IsUpgrading:      isUpgrading,
	}

	httpClient := &http.Client{Timeout: 8 * time.Second}
	targetURL := manifestURL
	if strings.Contains(targetURL, "?") {
		targetURL += fmt.Sprintf("&_t=%d", time.Now().UnixMilli())
	} else {
		targetURL += fmt.Sprintf("?_t=%d", time.Now().UnixMilli())
	}

	resp, err := httpClient.Get(targetURL)
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
	info.PublishedAt = manifest.PublishedAt
	info.ReleaseNotes = manifest.ReleaseNotes

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

// PrintCheckUpdate checks for remote updates and prints a clean 2-line status message.
func PrintCheckUpdate(appName, manifestURL, currentVersion, currentCommit, currentBuildTime string) error {
	info, err := CheckUpdate(manifestURL, currentVersion, currentCommit, currentBuildTime)
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

// ExecuteSelfUpgrade downloads the new binary, verifies SHA256 checksum, performs atomic replacement, and restarts.
func ExecuteSelfUpgrade(manifestURL, currentVersion string, force bool, isService bool) error {
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

	info, err := CheckUpdate(manifestURL, currentVersion, "", "")
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

	// Line 1: Found new release
	if currentVersion != "" && info.LatestVersion != "" && currentVersion != info.LatestVersion {
		fmt.Printf("Found new release: %s %s -> %s\n", strings.ToLower(appName), currentVersion, info.LatestVersion)
	} else {
		fmt.Printf("Reinstalling %s %s...\n", strings.ToLower(appName), info.LatestVersion)
	}

	tmpFile := execPath + ".upgrade.tmp"
	defer os.Remove(tmpFile)

	httpClient := &http.Client{Timeout: 120 * time.Second}
	downloadURL := info.DownloadURL
	if strings.Contains(downloadURL, "?") {
		downloadURL += fmt.Sprintf("&_t=%d", time.Now().UnixMilli())
	} else {
		downloadURL += fmt.Sprintf("?_t=%d", time.Now().UnixMilli())
	}

	resp, err := httpClient.Get(downloadURL)
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

	bakPath := execPath + ".bak"
	_ = os.Remove(bakPath)
	if err := copyFile(execPath, bakPath); err != nil {
		fmt.Println("failed")
		return fmt.Errorf("backup current binary failed: %w", err)
	}

	if err := os.Rename(tmpFile, execPath); err != nil {
		_ = os.Rename(bakPath, execPath)
		fmt.Println("failed")
		return fmt.Errorf("replace binary failed: %w", err)
	}
	fmt.Println("done")

	// Line 4: Success confirmation
	fmt.Printf("✓ Successfully upgraded to %s\n", info.LatestVersion)

	if isService && len(os.Args) > 0 {
		// Prevent infinite re-exec loop if invoked as a CLI upgrade command
		for _, a := range os.Args {
			lower := strings.ToLower(strings.TrimSpace(a))
			if lower == "upgrade" || lower == "update" {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Exec(execPath, os.Args, os.Environ())
	}

	return nil
}

// HandleUpgradeCmd checks command-line arguments for upgrade/check queries.
// Supported commands:
//   - "check", "check-update", "-check", "--check": inspects remote release without modifying files.
//   - "upgrade", "update": downloads, verifies, installs, and cleanly exits.
// Returns true if an upgrade command was handled, allowing main() to cleanly exit.
func HandleUpgradeCmd(appName, version, commit, buildTime, manifestURL string, isService ...bool) bool {
	if len(os.Args) > 1 {
		arg := strings.ToLower(strings.TrimSpace(os.Args[1]))

		switch arg {
		case "check", "check-update", "-check", "--check":
			_ = PrintCheckUpdate(appName, manifestURL, version, commit, buildTime)
			return true

		case "upgrade", "update":
			force := false
			for _, a := range os.Args[2:] {
				if a == "-f" || a == "--force" {
					force = true
				}
			}

			// CLI upgrade must exit cleanly and never re-exec the upgrade command
			if err := ExecuteSelfUpgrade(manifestURL, version, force, false); err != nil {
				fmt.Fprintf(os.Stderr, "error: upgrade failed: %v\n", err)
				os.Exit(1)
			}
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

