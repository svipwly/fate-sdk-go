package boot

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

func init() {
	// Automatically strip redundant Go timestamps for clean systemd/cloud-native logging
	log.SetFlags(0)
}

// ResolveCommit resolves git commit hash from Go build metadata if commit is empty or "dev".
func ResolveCommit(commit string) string {
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
	return commit
}

// FormatVersionTag formats a clean, concise version tag string for startup logs.
// Examples:
//   - "v0.1.0 (5a5b5c5)"
//   - "v0.1.0 (5a5b5c5-dirty)"
//   - "v0.1.0 (dev)"
func FormatVersionTag(version, commit string) string {
	commit = ResolveCommit(commit)
	return fmt.Sprintf("%s (%s)", version, commit)
}

// FormatVersionInfo formats comprehensive build and runtime metadata for CLI version commands.
// Example:
//   - "MyService v0.1.5 (commit: 3d16e91, built: 2026-09-07T16:48:00Z, go: go1.26.6, linux/amd64)"
func FormatVersionInfo(appName, version, commit, buildTime string) string {
	commit = ResolveCommit(commit)
	if buildTime == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range bi.Settings {
				if setting.Key == "vcs.time" {
					buildTime = setting.Value
					break
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

// PrintVersion outputs the standard standardized startup header line without duplicate timestamps:
// Example: [MyService] Version: v0.1.5 (3d16e91)
func PrintVersion(appName, version, commit string) {
	log.SetFlags(0)
	log.Printf("[%s] Version: %s", appName, FormatVersionTag(version, commit))
}

// HandleVersionCmd checks command line arguments for version queries:
//   - "version": standard modern subcommand (e.g. app version)
//   - "--version": standard GNU long flag (e.g. app --version)
// Returns true if matched so the caller can cleanly exit.
func HandleVersionCmd(appName, version, commit, buildTime string) bool {
	if len(os.Args) > 1 {
		arg := strings.ToLower(strings.TrimSpace(os.Args[1]))
		if arg == "version" || arg == "--version" {
			fmt.Println(FormatVersionInfo(appName, version, commit, buildTime))
			return true
		}
	}
	return false
}

// Command represents a CLI command or flag with its description for help generation.
type Command struct {
	Name        string
	Description string
}

// FormatHelpScreen produces a beautifully aligned, standard help message for any service in the ecosystem.
func FormatHelpScreen(appName, description string, customCommands ...Command) string {
	binName := filepath.Base(os.Args[0])
	if binName == "." || binName == "/" || binName == "" {
		binName = strings.ToLower(appName)
	}

	var allCmds []Command
	allCmds = append(allCmds, customCommands...)
	allCmds = append(allCmds, []Command{
		{Name: "(default)", Description: fmt.Sprintf("Run the %s service", appName)},
		{Name: "version, --version", Description: "Show version and build metadata"},
		{Name: "check", Description: "Check for new releases from CDN"},
		{Name: "upgrade [-f]", Description: "Self-upgrade to the latest version"},
		{Name: "help, -h, --help", Description: "Show this help message"},
	}...)

	maxLen := 0
	for _, c := range allCmds {
		if len(c.Name) > maxLen {
			maxLen = len(c.Name)
		}
	}

	var sb strings.Builder
	if description != "" {
		sb.WriteString(fmt.Sprintf("%s - %s\n\n", appName, description))
	} else {
		sb.WriteString(fmt.Sprintf("%s\n\n", appName))
	}
	sb.WriteString(fmt.Sprintf("Usage:\n  %s [command] [options]\n\nCommands:\n", binName))

	for _, c := range allCmds {
		sb.WriteString(fmt.Sprintf("  %-*s   %s\n", maxLen, c.Name, c.Description))
	}

	return sb.String()
}

// HandleHelpCmd checks if command-line arguments match "help", "-h", or "--help".
// If matched, it prints the formatted help message to stdout and returns true so main() can cleanly exit.
func HandleHelpCmd(appName, description string, customCommands ...Command) bool {
	if len(os.Args) > 1 {
		arg := strings.ToLower(strings.TrimSpace(os.Args[1]))
		if arg == "help" || arg == "-h" || arg == "--help" {
			fmt.Print(FormatHelpScreen(appName, description, customCommands...))
			return true
		}
	}
	return false
}
