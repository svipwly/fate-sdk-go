package boot

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
)

// FormatVersionTag formats a clean, concise version tag string for startup logs.
// Examples:
//   - "v0.1.0 (5a5b5c5)"
//   - "v0.1.0 (5a5b5c5-dirty)"
//   - "v0.1.0 (dev)"
func FormatVersionTag(version, commit string) string {
	if commit == "" || commit == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range bi.Settings {
				if setting.Key == "vcs.revision" {
					if len(setting.Value) > 7 {
						commit = setting.Value[:7]
					} else {
						commit = setting.Value
					}
				}
				if setting.Key == "vcs.modified" && setting.Value == "true" {
					if !strings.HasSuffix(commit, "-dirty") && commit != "" && commit != "dev" {
						commit += "-dirty"
					}
				}
			}
		}
	}
	if commit == "" {
		commit = "dev"
	}
	return fmt.Sprintf("%s (%s)", version, commit)
}

// FormatVersionInfo formats comprehensive build and runtime metadata for CLI version commands.
// Example:
//   - "MyService v0.1.5 (commit: 3d16e91, built: 2026-09-07T16:48:00Z, go: go1.26.6, linux/amd64)"
func FormatVersionInfo(appName, version, commit, buildTime string) string {
	if commit == "" || commit == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range bi.Settings {
				if setting.Key == "vcs.revision" {
					if len(setting.Value) > 7 {
						commit = setting.Value[:7]
					} else {
						commit = setting.Value
					}
				}
				if setting.Key == "vcs.time" && buildTime == "" {
					buildTime = setting.Value
				}
				if setting.Key == "vcs.modified" && setting.Value == "true" {
					if !strings.HasSuffix(commit, "-dirty") && commit != "" && commit != "dev" {
						commit += "-dirty"
					}
				}
			}
		}
	}

	var parts []string
	if commit != "" && commit != "dev" {
		parts = append(parts, fmt.Sprintf("commit: %s", commit))
	}
	if buildTime != "" {
		parts = append(parts, fmt.Sprintf("built: %s", buildTime))
	}
	parts = append(parts, fmt.Sprintf("go: %s", runtime.Version()))
	parts = append(parts, fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH))

	if len(parts) > 0 {
		return fmt.Sprintf("%s %s (%s)", appName, version, strings.Join(parts, ", "))
	}
	return fmt.Sprintf("%s %s", appName, version)
}

// PrintVersion outputs the standard standardized startup header line:
// Example: 2026/09/16 21:00:00 [MyService] Version: v0.1.5 (3d16e91)
func PrintVersion(appName, version, commit string) {
	log.Printf("[%s] Version: %s", appName, FormatVersionTag(version, commit))
}

// HandleVersionCmd checks command line arguments for version queries (-v, --version, version, -version).
// If matched, it prints full build info to stdout and returns true so the caller can cleanly exit.
func HandleVersionCmd(appName, version, commit, buildTime string) bool {
	if len(os.Args) > 1 {
		arg := os.Args[1]
		if arg == "-v" || arg == "--version" || arg == "version" || arg == "-version" {
			fmt.Println(FormatVersionInfo(appName, version, commit, buildTime))
			return true
		}
	}
	return false
}
