package env

import (
	"os"
	"strconv"
	"strings"
)

// Get retrieves a string environment variable or returns defaultVal if empty.
func Get(key, defaultVal string) string {
	if val := os.Getenv(key); strings.TrimSpace(val) != "" {
		return val
	}
	return defaultVal
}

// GetInt retrieves an integer environment variable or returns defaultVal if missing or invalid.
func GetInt(key string, defaultVal int) int {
	valStr := strings.TrimSpace(os.Getenv(key))
	if valStr == "" {
		return defaultVal
	}
	if val, err := strconv.Atoi(valStr); err == nil {
		return val
	}
	return defaultVal
}

// GetBool retrieves a boolean environment variable or returns defaultVal if missing or invalid.
// Supported true values: "1", "t", "T", "true", "TRUE", "True"
// Supported false values: "0", "f", "F", "false", "FALSE", "False"
func GetBool(key string, defaultVal bool) bool {
	valStr := strings.TrimSpace(os.Getenv(key))
	if valStr == "" {
		return defaultVal
	}
	if val, err := strconv.ParseBool(valStr); err == nil {
		return val
	}
	return defaultVal
}

// GetListenAddr retrieves the standardized LISTEN_ADDR environment variable.
// If unset or empty, it returns defaultAddr (e.g. "127.0.0.1:33004").
// If only a port number or ":port" is provided, it normalizes with "127.0.0.1".
func GetListenAddr(defaultAddr string) string {
	addr := strings.TrimSpace(os.Getenv("LISTEN_ADDR"))
	if addr == "" {
		return defaultAddr
	}

	// If only port (e.g., "33004"), prepend "127.0.0.1:"
	if !strings.Contains(addr, ":") {
		return "127.0.0.1:" + addr
	}

	// If ":33004", normalize to "127.0.0.1:33004" for explicit loopback binding
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}

	return addr
}
