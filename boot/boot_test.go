package boot

import (
	"os"
	"strings"
	"testing"
)

func TestFormatVersionTag(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		commit   string
		expected string
	}{
		{
			name:     "clean commit",
			version:  "v0.1.0",
			commit:   "5a5b5c5",
			expected: "v0.1.0 (5a5b5c5)",
		},
		{
			name:     "dev fallback",
			version:  "v1.0.0",
			commit:   "",
			expected: "v1.0.0 (dev)",
		},
		{
			name:     "explicit dev",
			version:  "v2.0.0",
			commit:   "dev",
			expected: "v2.0.0 (dev)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := FormatVersionTag(tc.version, tc.commit)
			if actual != tc.expected {
				t.Errorf("FormatVersionTag(%q, %q) = %q; expected %q", tc.version, tc.commit, actual, tc.expected)
			}
		})
	}
}

func TestFormatVersionInfo(t *testing.T) {
	out := FormatVersionInfo("ExampleApp", "v0.1.5", "3d16e91", "2026-09-07T16:48:00Z")
	if !strings.HasPrefix(out, "ExampleApp v0.1.5") {
		t.Errorf("expected output to start with 'ExampleApp v0.1.5', got %q", out)
	}
	if !strings.Contains(out, "commit: 3d16e91") {
		t.Errorf("expected output to contain commit info, got %q", out)
	}
}

func TestHandleVersionCmd(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	// "version" subcommand -> true
	os.Args = []string{"app", "version"}
	if !HandleVersionCmd("App", "v1.0.0", "abc", "") {
		t.Errorf("expected 'version' subcommand to be handled")
	}

	// "--version" long flag -> true
	os.Args = []string{"app", "--version"}
	if !HandleVersionCmd("App", "v1.0.0", "abc", "") {
		t.Errorf("expected '--version' flag to be handled")
	}

	// "-v" should NOT trigger version anymore (reserved for verbose)
	os.Args = []string{"app", "-v"}
	if HandleVersionCmd("App", "v1.0.0", "abc", "") {
		t.Errorf("expected '-v' to not be handled as version subcommand")
	}

	// unknown command -> false
	os.Args = []string{"app", "serve"}
	if HandleVersionCmd("App", "v1.0.0", "abc", "") {
		t.Errorf("expected 'serve' to not be handled")
	}
}
