package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareSemVer(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"v0.1.1", "v0.1.0", 1},
		{"v0.1.0", "v0.1.2", -1},
		{"v1.0.0", "v0.9.9", 1},
		{"v0.2.0-rc1", "v0.1.9", 1},
	}

	for _, tc := range tests {
		actual := CompareSemVer(tc.v1, tc.v2)
		if actual != tc.expected {
			t.Errorf("CompareSemVer(%q, %q) = %d; expected %d", tc.v1, tc.v2, actual, tc.expected)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	manifest := ReleaseManifest{
		Name:        "TestApp",
		Version:     "v0.2.0",
		PublishedAt: "2026-09-16T20:00:00Z",
		Platforms: map[string]PlatformDetail{
			"linux-amd64": {
				Filename: "testapp-linux-amd64",
				URL:      "http://example.com/testapp",
				SHA256:   "abcd",
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer ts.Close()

	info, err := CheckUpdate(ts.URL, "v0.1.0", "dev", "")
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}

	if info.AppName != "TestApp" {
		t.Errorf("expected app name TestApp, got %s", info.AppName)
	}
	if !info.CanUpdate {
		t.Errorf("expected CanUpdate = true for v0.1.0 -> v0.2.0")
	}
}

func TestPrintCheckUpdate(t *testing.T) {
	manifest := ReleaseManifest{
		Name:        "TestApp",
		Version:     "v0.2.0",
		PublishedAt: "2026-09-16T20:00:00Z",
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(manifest)
	}))
	defer ts.Close()

	if err := PrintCheckUpdate("TestApp", ts.URL, "v0.1.0", "dev", ""); err != nil {
		t.Fatalf("PrintCheckUpdate failed: %v", err)
	}
	if err := PrintCheckUpdate("TestApp", ts.URL, "v0.2.0", "dev", ""); err != nil {
		t.Fatalf("PrintCheckUpdate up to date failed: %v", err)
	}
}

