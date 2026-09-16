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

	"github.com/svipwly/fate-sdk-go/boot"
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

// PrintCheckUpdate checks for remote updates and prints a clean user message to stdout.
func PrintCheckUpdate(appName, manifestURL, currentVersion, currentCommit, currentBuildTime string) error {
	info, err := CheckUpdate(manifestURL, currentVersion, currentCommit, currentBuildTime)
	if err != nil {
		return fmt.Errorf("check update failed: %w", err)
	}

	if info.CanUpdate {
		fmt.Printf("🚀 %s update available: %s -> %s\nRun '%s upgrade' to update.\n", appName, currentVersion, info.LatestVersion, appName)
	} else {
		fmt.Printf("✓ %s %s is up to date\n", appName, currentVersion)
	}
	return nil
}

// ExecuteSelfUpgrade downloads the new binary, verifies SHA256 checksum, performs atomic replacement, and restarts.
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

	info, err := CheckUpdate(manifestURL, currentVersion, "", "")
	if err != nil {
		return fmt.Errorf("check update failed: %w", err)
	}

	if !info.CanUpdate && !force {
		return fmt.Errorf("already up to date (%s)", currentVersion)
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

	tmpFile := execPath + ".tmp"
	defer os.Remove(tmpFile)

	fmt.Printf("[Updater] Downloading %s (%s) from %s ...\n", info.AppName, info.LatestVersion, info.DownloadURL)
	httpClient := &http.Client{Timeout: 120 * time.Second}
	resp, err := httpClient.Get(info.DownloadURL)
	if err != nil {
		return fmt.Errorf("download binary failed: %w", err)
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
	writer := io.MultiWriter(out, hasher)
	if _, err := io.Copy(writer, resp.Body); err != nil {
		out.Close()
		return fmt.Errorf("write binary failed: %w", err)
	}
	_ = out.Sync()
	out.Close()

	if info.SHA256 != "" {
		computedSHA := hex.EncodeToString(hasher.Sum(nil))
		if !strings.EqualFold(computedSHA, info.SHA256) {
			return fmt.Errorf("checksum mismatch: expected %s, got %s", info.SHA256, computedSHA)
		}
		fmt.Printf("[Updater] ✓ Checksum verified (SHA256: %s)\n", computedSHA)
	}

	oldFile := execPath + ".old"
	_ = os.Remove(oldFile)
	if err := os.Rename(execPath, oldFile); err != nil {
		return fmt.Errorf("backup current binary failed: %w", err)
	}

	if err := os.Rename(tmpFile, execPath); err != nil {
		_ = os.Rename(oldFile, execPath)
		return fmt.Errorf("replace binary failed: %w", err)
	}
	_ = os.Remove(oldFile)

	fmt.Printf("[Updater] ✨ Successfully upgraded %s to %s!\n", info.AppName, info.LatestVersion)

	if len(os.Args) > 0 {
		fmt.Printf("[Updater] Restarting process %s ...\n", execPath)
		_ = syscall.Exec(execPath, os.Args, os.Environ())
	}
	return nil
}

// RunSelfUpgrade is a CLI helper that handles self-upgrade arguments and terminal output.
func RunSelfUpgrade(appName, version, commit, buildTime, manifestURL string, args []string) {
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
		}
	}

	fmt.Printf("[Updater] Current version: %s\n", boot.FormatVersionTag(version, commit))
	fmt.Printf("[Updater] Checking for updates from %s ...\n", manifestURL)

	if err := ExecuteSelfUpgrade(manifestURL, version, force); err != nil {
		fmt.Fprintf(os.Stderr, "[Updater] Upgrade failed: %v\n", err)
		os.Exit(1)
	}
}

// RunCheckUpdate is a CLI helper that prints update status and exits.
func RunCheckUpdate(appName, version, commit, buildTime, manifestURL string, args []string) {
	if err := PrintCheckUpdate(appName, manifestURL, version, commit, buildTime); err != nil {
		fmt.Fprintf(os.Stderr, "Check update error: %v\n", err)
		os.Exit(1)
	}
}
