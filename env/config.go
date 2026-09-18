package env

import (
	"strings"
	"time"
)

// ServerConfig represents the standard server network configuration.
type ServerConfig struct {
	ListenAddr string
}

// LoadServer loads the ServerConfig from LISTEN_ADDR with default fallback.
func LoadServer(defaultAddr string) ServerConfig {
	return ServerConfig{
		ListenAddr: GetListenAddr(defaultAddr),
	}
}

// DBConfig represents standard database configuration supporting both SQLite and PostgreSQL.
type DBConfig struct {
	Type            string        // "sqlite" or "postgres"
	Path            string        // SQLite file path
	DSN             string        // PostgreSQL connection string
	MaxOpenConns    int           // Maximum open connections
	MaxIdleConns    int           // Maximum idle connections
	ConnMaxLifetime time.Duration // Connection max lifetime
}

// LoadDB loads DBConfig from environment variables.
func LoadDB(defaultPath string) DBConfig {
	dbType := strings.ToLower(strings.TrimSpace(Get("DB_TYPE", "sqlite")))
	dsn := strings.TrimSpace(Get("DB_DSN", ""))

	// Reasonable connection pool defaults based on database engine
	defaultMaxOpen := 1
	defaultMaxIdle := 1
	if dbType == "postgres" {
		defaultMaxOpen = 25
		defaultMaxIdle = 5
	}

	return DBConfig{
		Type:            dbType,
		Path:            Get("DB_PATH", defaultPath),
		DSN:             dsn,
		MaxOpenConns:    GetInt("DB_MAX_OPEN_CONNS", defaultMaxOpen),
		MaxIdleConns:    GetInt("DB_MAX_IDLE_CONNS", defaultMaxIdle),
		ConnMaxLifetime: time.Duration(GetInt("DB_CONN_MAX_LIFETIME", 300)) * time.Second,
	}
}

// SSOConfig represents standard SSOID / OIDC client connection parameters.
type SSOConfig struct {
	BrandName    string
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURI  string
}

// LoadSSO loads SSOConfig from standardized SSOID_* environment variables.
func LoadSSO(defaultClientID, defaultRedirectURI string) SSOConfig {
	return SSOConfig{
		BrandName:    Get("SSOID_BRAND_NAME", "SSOID"),
		IssuerURL:    strings.TrimRight(Get("SSOID_ISSUER_URL", ""), "/"),
		ClientID:     Get("SSOID_CLIENT_ID", defaultClientID),
		ClientSecret: Get("SSOID_CLIENT_SECRET", ""),
		RedirectURI:  Get("SSOID_REDIRECT_URI", defaultRedirectURI),
	}
}

// Enabled returns true if SSO provider URL and ClientID are configured.
func (c SSOConfig) Enabled() bool {
	return c.IssuerURL != "" && c.ClientID != ""
}

// S3Config represents standard S3 / R2 object storage configuration.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
}

// LoadS3 loads S3Config from standardized S3_* environment variables.
func LoadS3() S3Config {
	return S3Config{
		Endpoint:        Get("S3_ENDPOINT", ""),
		Region:          Get("S3_REGION", "us-east-1"),
		Bucket:          Get("S3_BUCKET", ""),
		AccessKeyID:     Get("S3_ACCESS_KEY_ID", ""),
		SecretAccessKey: Get("S3_SECRET_ACCESS_KEY", ""),
		UsePathStyle:    GetBool("S3_USE_PATH_STYLE", false),
	}
}

// IsConfigured returns true if required S3 parameters are set.
func (c S3Config) IsConfigured() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// LogConfig represents standard logging configuration.
type LogConfig struct {
	Level  string // debug, info, warn, error
	Format string // text, json
}

// LoadLog loads LogConfig from environment variables.
func LoadLog() LogConfig {
	return LogConfig{
		Level:  strings.ToLower(Get("LOG_LEVEL", "info")),
		Format: strings.ToLower(Get("LOG_FORMAT", "text")),
	}
}
