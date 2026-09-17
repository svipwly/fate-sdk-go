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
	"runtime/debug"
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
	Name      string                    `json:"name"`
	Version   string                    `json:"version"`
	Commit    string                    `json:"commit,omitempty"`
	Platforms map[string]PlatformDetail `json:"platforms"`
}

// SystemVersionInfo represents runtime version details and upgrade availability.
type SystemVersionInfo struct {
	AppName        string `json:"app_name"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Commit         string `json:"commit,omitempty"`
	CanUpdate      bool   `json:"can_update"`
	DownloadURL    string `json:"download_url,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
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
	info.Commit = manifest.Commit

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

func getLocalCommit() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range bi.Settings {
			if setting.Key == "vcs.revision" {
				if len(setting.Value) > 7 {
					return setting.Value[:7]
				}
				return setting.Value
			}
		}
	}
	return ""
}

// PrintCheckUpdate checks for remote updates and prints a clean status message.
func PrintCheckUpdate(appName, manifestURL, currentVersion string) error {
	info, err := CheckUpdate(manifestURL, currentVersion)
	if err != nil {
		return fmt.Errorf("check update failed: %w", err)
	}

	cmdName := strings.ToLower(appName)
	localCommit := getLocalCommit()
	targetCommit := info.Commit
	if len(targetCommit) > 7 {
		targetCommit = targetCommit[:7]
	}

	formatVerWithCommit := func(ver, commit string) string {
		if commit != "" {
			return fmt.Sprintf("%s (%s)", ver, commit)
		}
		return ver
	}

	if info.CanUpdate {
		fmt.Printf("%s update available: %s -> %s\nRun '%s upgrade' to update.\n",
			cmdName,
			formatVerWithCommit(currentVersion, localCommit),
			formatVerWithCommit(info.LatestVersion, targetCommit),
			cmdName,
		)
	} else if localCommit != "" && targetCommit != "" && localCommit != targetCommit {
		fmt.Printf("%s new build available: %s (%s -> %s)\nRun '%s upgrade -f' to reinstall.\n",
			cmdName,
			info.LatestVersion,
			localCommit,
			targetCommit,
			cmdName,
		)
	} else {
		commit := targetCommit
		if commit == "" {
			commit = localCommit
		}
		if commit != "" {
			fmt.Printf("✓ %s is up to date: %s (%s)\n", cmdName, currentVersion, commit)
		} else {
			fmt.Printf("✓ %s is up to date: %s\n", cmdName, currentVersion)
		}
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
		localCommit := getLocalCommit()
		targetCommit := info.Commit
		if len(targetCommit) > 7 {
			targetCommit = targetCommit[:7]
		}
		cmdName := strings.ToLower(info.AppName)
		if localCommit != "" && targetCommit != "" && localCommit != targetCommit {
			fmt.Printf("%s new build available: %s (%s -> %s)\nRun '%s upgrade -f' to reinstall.\n",
				cmdName,
				info.LatestVersion,
				localCommit,
				targetCommit,
				cmdName,
			)
			return nil
		}
		commit := targetCommit
		if commit == "" {
			commit = localCommit
		}
		if commit != "" {
			fmt.Printf("✓ %s is up to date: %s (%s)\n", cmdName, currentVersion, commit)
		} else {
			fmt.Printf("✓ %s is up to date: %s\n", cmdName, currentVersion)
		}
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

	// Line 1: Dynamic download progress
	prefix := fmt.Sprintf("• Downloading %s %s...", strings.ToLower(appName), info.LatestVersion)
	if resp.ContentLength > 0 {
		sizeMB := float64(resp.ContentLength) / (1024 * 1024)
		prefix = fmt.Sprintf("• Downloading %s %s (%.1f MB)...", strings.ToLower(appName), info.LatestVersion, sizeMB)
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

	if info.SHA256 != "" {
		computedSHA := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(computedSHA, info.SHA256) {
			targetCommit := info.Commit
			if len(targetCommit) > 7 {
				targetCommit = targetCommit[:7]
			}
			verTag := info.LatestVersion
			if targetCommit != "" {
				verTag = fmt.Sprintf("%s (%s)", info.LatestVersion, targetCommit)
			}
			return fmt.Errorf("checksum mismatch\ntarget: %s\nexpected: %s\ngot: %s", verTag, info.SHA256, computedSHA)
		}
	}

	// Line 2: Install and restart service (if active)
	svcName := strings.ToLower(appName)
	systemctlPath, hasActiveService := isSystemdServiceActive(svcName)
	if hasActiveService {
		fmt.Printf("• Installing and restarting service (%s)... ", svcName)
	} else {
		fmt.Print("• Installing binary... ")
	}

	_ = os.Chmod(tmpFile, 0755)
	if err := os.Rename(tmpFile, execPath); err != nil {
		fmt.Println("failed")
		return fmt.Errorf("replace binary failed: %w", err)
	}
	_ = os.Chmod(execPath, 0755)

	if hasActiveService {
		if err := exec.Command(systemctlPath, "restart", svcName).Run(); err != nil {
			fmt.Printf("failed\n  Notice: Please run 'systemctl restart %s' manually (%v).\n", svcName, err)
		} else {
			fmt.Println("done")
		}
	} else {
		fmt.Println("done")
	}

	// Line 3: Final confirmation
	isSameVersion := currentVersion != "" && info.LatestVersion != "" && currentVersion == info.LatestVersion
	if isSameVersion {
		localCommit := getLocalCommit()
		targetCommit := info.Commit
		if len(targetCommit) > 7 {
			targetCommit = targetCommit[:7]
		}
		commitTag := ""
		if localCommit != "" && targetCommit != "" && localCommit != targetCommit {
			commitTag = fmt.Sprintf(" (%s -> %s)", localCommit, targetCommit)
		} else if targetCommit != "" {
			commitTag = fmt.Sprintf(" (%s)", targetCommit)
		} else if localCommit != "" {
			commitTag = fmt.Sprintf(" (%s)", localCommit)
		}
		fmt.Printf("✓ Successfully reinstalled: %s%s\n", info.LatestVersion, commitTag)
	} else {
		localCommit := getLocalCommit()
		targetCommit := info.Commit
		if len(targetCommit) > 7 {
			targetCommit = targetCommit[:7]
		}
		formatVerWithCommit := func(ver, commit string) string {
			if commit != "" {
				return fmt.Sprintf("%s (%s)", ver, commit)
			}
			return ver
		}
		if currentVersion != "" && currentVersion != "dev" {
			fmt.Printf("✓ Successfully upgraded: %s -> %s\n",
				formatVerWithCommit(currentVersion, localCommit),
				formatVerWithCommit(info.LatestVersion, targetCommit),
			)
		} else {
			fmt.Printf("✓ Successfully upgraded: %s\n",
				formatVerWithCommit(info.LatestVersion, targetCommit),
			)
		}
	}

	return nil
}

func isSystemdServiceActive(serviceName string) (string, bool) {
	if runtime.GOOS != "linux" || serviceName == "" {
		return "", false
	}
	systemctlPath, err := exec.LookPath("systemctl")
	if err != nil {
		return "", false
	}
	if err := exec.Command(systemctlPath, "is-active", "--quiet", serviceName).Run(); err == nil {
		return systemctlPath, true
	}
	return "", false
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

// InProcessUpgradeReq defines parameters for headless in-process self-upgrades.
type InProcessUpgradeReq struct {
	TargetVersion string
	DownloadURL   string
	SHA256        string
	Force         bool
	RestartDelay  time.Duration
}

// PerformInProcessUpgrade performs a headless, silent in-process self-upgrade.
// It downloads the target binary, verifies SHA256, atomically replaces the running binary,
// and schedules a graceful syscall.Exec restart in the background.
func PerformInProcessUpgrade(manifestURL, currentVersion string, req InProcessUpgradeReq) error {
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

	downloadURL := strings.TrimSpace(req.DownloadURL)
	expectedSHA := strings.TrimSpace(req.SHA256)
	targetVersion := strings.TrimSpace(req.TargetVersion)

	if downloadURL == "" || expectedSHA == "" || targetVersion == "" {
		info, err := CheckUpdate(manifestURL, currentVersion)
		if err != nil {
			return fmt.Errorf("check update failed: %w", err)
		}
		if downloadURL == "" {
			downloadURL = info.DownloadURL
		}
		if expectedSHA == "" {
			expectedSHA = info.SHA256
		}
		if targetVersion == "" {
			targetVersion = info.LatestVersion
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no release binary found for current platform (%s-%s)", runtime.GOOS, runtime.GOARCH)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable failed: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks failed: %w", err)
	}

	localSHA, _ := calculateFileSHA256(execPath)
	isSameVersion := strings.TrimPrefix(strings.ToLower(targetVersion), "v") == strings.TrimPrefix(strings.ToLower(currentVersion), "v") && currentVersion != "" && currentVersion != "dev"
	isSameSHA := localSHA != "" && expectedSHA != "" && strings.EqualFold(localSHA, expectedSHA)

	if isSameVersion && isSameSHA && !req.Force {
		shortSHA := localSHA
		if len(shortSHA) > 8 {
			shortSHA = shortSHA[:8]
		}
		return fmt.Errorf("already running latest binary (version %s, sha: %s)", currentVersion, shortSHA)
	}

	tmpFile := execPath + ".upgrade.tmp"
	_ = os.Remove(tmpFile)
	defer os.Remove(tmpFile)

	httpClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := httpClient.Get(bustCache(downloadURL))
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

	hasher := sha256.New()
	multiWriter := io.MultiWriter(out, hasher)

	if _, err := io.Copy(multiWriter, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("stream copy failed: %w", err)
	}
	_ = out.Sync()
	out.Close()

	computedSHA := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA != "" && !strings.EqualFold(computedSHA, expectedSHA) {
		return fmt.Errorf("checksum verification failed (expected %s, got %s)", expectedSHA, computedSHA)
	}

	_ = os.Chmod(tmpFile, 0755)
	if err := os.Rename(tmpFile, execPath); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}
	_ = os.Chmod(execPath, 0755)

	delay := req.RestartDelay
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}

	go func() {
		time.Sleep(delay)
		_ = syscall.Exec(execPath, os.Args, os.Environ())
	}()

	return nil
}

func calculateFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

