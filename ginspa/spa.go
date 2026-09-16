package ginspa

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// Config defines configuration options for serving Single Page Applications (SPA).
type Config struct {
	// IndexFile is the default HTML entrypoint (default: "index.html").
	IndexFile string
	// ExcludePrefixes defines URL prefixes that should return 404 instead of falling back to index.html (e.g. "/api").
	ExcludePrefixes []string
}

// DefaultConfig returns recommended production defaults for SPA serving.
func DefaultConfig() Config {
	return Config{
		IndexFile: "index.html",
		ExcludePrefixes: []string{
			"/api",
			"/auth",
			"/oauth2",
			"/stream",
		},
	}
}

// Serve mounts an embedded filesystem (fs.FS) onto a Gin engine for SPA web applications.
// It handles:
//   - Root and client-side route fallback to index.html with no-cache headers.
//   - Static asset streaming with long-term immutable caching headers.
//   - Automatic MIME type resolution based on file extensions.
//   - 404 JSON fallback for excluded backend API route prefixes.
func Serve(engine *gin.Engine, fsys fs.FS, customConfig ...Config) {
	cfg := DefaultConfig()
	if len(customConfig) > 0 {
		if customConfig[0].IndexFile != "" {
			cfg.IndexFile = customConfig[0].IndexFile
		}
		if len(customConfig[0].ExcludePrefixes) > 0 {
			cfg.ExcludePrefixes = customConfig[0].ExcludePrefixes
		}
	}

	handler := Handler(fsys, cfg)
	engine.GET("/", handler)
	engine.NoRoute(handler)
}

// Handler returns a standalone Gin handler for SPA serving.
func Handler(fsys fs.FS, cfg Config) gin.HandlerFunc {
	if cfg.IndexFile == "" {
		cfg.IndexFile = "index.html"
	}

	return func(c *gin.Context) {
		reqPath := strings.TrimPrefix(c.Request.URL.Path, "/")

		// Check if request matches excluded backend API prefixes
		for _, prefix := range cfg.ExcludePrefixes {
			cleanPrefix := strings.TrimPrefix(prefix, "/")
			if strings.HasPrefix(reqPath, cleanPrefix) {
				c.JSON(http.StatusNotFound, gin.H{
					"error":   "not_found",
					"message": "Endpoint not found",
				})
				return
			}
		}

		// Root request -> serve index.html with no-cache
		if reqPath == "" {
			serveIndexHTML(c, fsys, cfg.IndexFile)
			return
		}

		cleanPath := path.Clean(reqPath)

		// Try to open static asset from filesystem
		file, err := fsys.Open(cleanPath)
		if err == nil {
			defer file.Close()
			stat, err := file.Stat()
			if err == nil && !stat.IsDir() {
				serveStaticAsset(c, cleanPath, file)
				return
			}
		}

		// If path looks like a missing static asset with extension (e.g. bundle.js, image.png), return 404
		if IsStaticAsset(cleanPath) {
			c.Status(http.StatusNotFound)
			return
		}

		// SPA route fallback -> serve index.html with no-cache
		serveIndexHTML(c, fsys, cfg.IndexFile)
	}
}

// IsStaticAsset checks if the file path has a static file extension.
func IsStaticAsset(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".js", ".mjs", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".woff", ".woff2", ".ttf", ".eot", ".json", ".wasm", ".map", ".txt", ".mp3", ".flac", ".ogg":
		return true
	default:
		return false
	}
}

func serveIndexHTML(c *gin.Context, fsys fs.FS, indexFile string) {
	data, err := fs.ReadFile(fsys, indexFile)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func serveStaticAsset(c *gin.Context, assetPath string, file fs.File) {
	data, err := fs.ReadFile(fsysFromFile(file), assetPath)
	if err != nil {
		// Fallback reading directly if possible
		c.Status(http.StatusNotFound)
		return
	}

	ext := path.Ext(assetPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, contentType, data)
}

// Single-file read helper
type singleFS struct {
	file fs.File
}

func fsysFromFile(f fs.File) fs.FS {
	return directReaderFS{file: f}
}

type directReaderFS struct {
	file fs.File
}

func (d directReaderFS) Open(name string) (fs.File, error) {
	return d.file, nil
}
