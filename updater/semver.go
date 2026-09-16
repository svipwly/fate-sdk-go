package updater

import (
	"strconv"
	"strings"
)

// CompareSemVer compares two Semantic Version strings (e.g. "v0.1.2" and "v0.1.3").
// Returns:
//   - 1 if v1 > v2
//   - -1 if v1 < v2
//   - 0 if v1 == v2
func CompareSemVer(v1, v2 string) int {
	parts1 := parseSemVer(v1)
	parts2 := parseSemVer(v2)

	for i := 0; i < 3; i++ {
		if parts1[i] > parts2[i] {
			return 1
		}
		if parts1[i] < parts2[i] {
			return -1
		}
	}
	return 0
}

func parseSemVer(v string) [3]int {
	var result [3]int
	cleaned := strings.TrimPrefix(strings.TrimSpace(v), "v")
	if idx := strings.Index(cleaned, "-"); idx != -1 {
		cleaned = cleaned[:idx]
	}
	if idx := strings.Index(cleaned, "+"); idx != -1 {
		cleaned = cleaned[:idx]
	}

	segments := strings.Split(cleaned, ".")
	for i := 0; i < len(segments) && i < 3; i++ {
		val, err := strconv.Atoi(segments[i])
		if err == nil {
			result[i] = val
		}
	}
	return result
}
