package env

import (
	"os"
	"testing"
	"time"
)

func TestEnvHelpers(t *testing.T) {
	os.Setenv("TEST_STRING", "hello")
	os.Setenv("TEST_INT", "42")
	os.Setenv("TEST_BOOL_TRUE", "true")
	os.Setenv("TEST_BOOL_FALSE", "false")
	defer func() {
		os.Unsetenv("TEST_STRING")
		os.Unsetenv("TEST_INT")
		os.Unsetenv("TEST_BOOL_TRUE")
		os.Unsetenv("TEST_BOOL_FALSE")
	}()

	if val := Get("TEST_STRING", "default"); val != "hello" {
		t.Errorf("expected 'hello', got '%s'", val)
	}
	if val := Get("TEST_NOT_SET", "default"); val != "default" {
		t.Errorf("expected 'default', got '%s'", val)
	}

	if val := GetInt("TEST_INT", 0); val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
	if val := GetInt("TEST_NOT_SET", 10); val != 10 {
		t.Errorf("expected 10, got %d", val)
	}

	if val := GetBool("TEST_BOOL_TRUE", false); val != true {
		t.Errorf("expected true, got %v", val)
	}
	if val := GetBool("TEST_BOOL_FALSE", true); val != false {
		t.Errorf("expected false, got %v", val)
	}
}

func TestGetListenAddr(t *testing.T) {
	// 1. Unset
	os.Unsetenv("LISTEN_ADDR")
	if addr := GetListenAddr("127.0.0.1:33004"); addr != "127.0.0.1:33004" {
		t.Errorf("expected '127.0.0.1:33004', got '%s'", addr)
	}

	// 2. Full IP:Port
	os.Setenv("LISTEN_ADDR", "0.0.0.0:8080")
	if addr := GetListenAddr("127.0.0.1:33004"); addr != "0.0.0.0:8080" {
		t.Errorf("expected '0.0.0.0:8080', got '%s'", addr)
	}

	// 3. Port only
	os.Setenv("LISTEN_ADDR", "33004")
	if addr := GetListenAddr("127.0.0.1:33004"); addr != "127.0.0.1:33004" {
		t.Errorf("expected '127.0.0.1:33004', got '%s'", addr)
	}

	// 4. Leading colon
	os.Setenv("LISTEN_ADDR", ":33004")
	if addr := GetListenAddr("127.0.0.1:33004"); addr != "127.0.0.1:33004" {
		t.Errorf("expected '127.0.0.1:33004', got '%s'", addr)
	}
	os.Unsetenv("LISTEN_ADDR")
}

func TestLoadLoaders(t *testing.T) {
	// Test ServerConfig
	srv := LoadServer("127.0.0.1:33004")
	if srv.ListenAddr != "127.0.0.1:33004" {
		t.Errorf("unexpected listen addr: %s", srv.ListenAddr)
	}

	// Test SSOConfig
	os.Setenv("SSOID_BRAND_NAME", "MyBrand")
	os.Setenv("SSOID_ISSUER_URL", "https://sso.test.com/")
	os.Setenv("SSOID_CLIENT_ID", "my-app")
	os.Setenv("SSOID_CLIENT_SECRET", "secret")
	os.Setenv("SSOID_REDIRECT_URI", "https://app.test.com/callback")
	defer func() {
		os.Unsetenv("SSOID_BRAND_NAME")
		os.Unsetenv("SSOID_ISSUER_URL")
		os.Unsetenv("SSOID_CLIENT_ID")
		os.Unsetenv("SSOID_CLIENT_SECRET")
		os.Unsetenv("SSOID_REDIRECT_URI")
	}()

	sso := LoadSSO("default-app", "http://default/callback")
	if sso.BrandName != "MyBrand" {
		t.Errorf("expected 'MyBrand', got '%s'", sso.BrandName)
	}
	if sso.IssuerURL != "https://sso.test.com" {
		t.Errorf("expected trimmed 'https://sso.test.com', got '%s'", sso.IssuerURL)
	}
	if !sso.Enabled() {
		t.Errorf("expected sso to be enabled")
	}

	// Test S3Config
	os.Setenv("S3_ENDPOINT", "https://s3.test.com")
	os.Setenv("S3_BUCKET", "bucket1")
	os.Setenv("S3_ACCESS_KEY_ID", "key1")
	os.Setenv("S3_SECRET_ACCESS_KEY", "sec1")
	os.Setenv("S3_USE_PATH_STYLE", "false")
	defer func() {
		os.Unsetenv("S3_ENDPOINT")
		os.Unsetenv("S3_BUCKET")
		os.Unsetenv("S3_ACCESS_KEY_ID")
		os.Unsetenv("S3_SECRET_ACCESS_KEY")
		os.Unsetenv("S3_USE_PATH_STYLE")
	}()

	s3 := LoadS3()
	if !s3.IsConfigured() {
		t.Errorf("expected s3 to be configured")
	}
	if s3.UsePathStyle != false {
		t.Errorf("expected use path style false")
	}

	// Test DBConfig
	db := LoadDB("./data/app.db")
	if db.Type != "sqlite" || db.Path != "./data/app.db" || db.MaxOpenConns != 1 || db.ConnMaxLifetime != 300*time.Second {
		t.Errorf("unexpected db config: %+v", db)
	}

	// Test LogConfig
	logCfg := LoadLog()
	if logCfg.Level != "info" || logCfg.Format != "text" {
		t.Errorf("unexpected log config: %+v", logCfg)
	}
}
