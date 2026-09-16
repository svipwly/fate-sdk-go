# fate-sdk-go

Standard Go SDK and core infrastructure utilities for cloud-native microservices and CLI applications.

[![Go Reference](https://pkg.go.dev/badge/github.com/svipwly/fate-sdk-go.svg)](https://pkg.go.dev/github.com/svipwly/fate-sdk-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Features

* **`boot`**: Standardized service startup headers, build metadata reflection (Git commit hash, dirty status, Go runtime), and `-v` / `--version` CLI command handlers.
* **`updater`**: Zero-dependency self-update engine supporting atomic binary replacement, SHA256 checksum verification, and SemVer comparisons with release CDNs.
* **`ginspa`**: High-performance Single Page Application (SPA) embedded static file server for Gin with automatic immutable caching, no-cache headers for `index.html`, and client-side routing fallback.
* **`oidc`**: Lightweight OpenID Connect / OAuth 2.0 authentication client, token verification with memory caching, and session management.

---

## Installation

```bash
go get github.com/svipwly/fate-sdk-go
```

---

## Quick Start

### 1. Standard Startup Logs & Version Handling (`boot`)

```go
package main

import (
	"github.com/svipwly/fate-sdk-go/boot"
)

var (
	Version   = "v0.1.0"
	Commit    = "dev"
	BuildTime = ""
)

func main() {
	// 1. Intercept CLI version queries (-v, --version, version)
	if boot.HandleVersionCmd("MyService", Version, Commit, BuildTime) {
		return
	}

	// 2. Output standardized startup log header
	// Output: 2026/09/16 21:00:00 [MyService] Version: v0.1.0 (5a5b5c5)
	boot.PrintVersion("MyService", Version, Commit)

	// ... Initialize database, routes, and start HTTP server
}
```

### 2. Embedded Frontend SPA Serving (`ginspa`)

```go
package main

import (
	"embed"

	"github.com/gin-gonic/gin"
	"github.com/svipwly/fate-sdk-go/ginspa"
)

//go:embed dist/*
var staticFS embed.FS

func main() {
	router := gin.Default()

	// Mount embedded frontend SPA with 1 line
	ginspa.Serve(router, staticFS)

	router.Run(":8080")
}
```

### 3. OpenID Connect / SSO Authentication (`oidc`)

```go
package main

import (
	"context"
	"log"

	"github.com/svipwly/fate-sdk-go/oidc"
)

func main() {
	client := oidc.NewClient(oidc.Config{
		IssuerURL:    "https://auth.example.com",
		ClientID:     "my-service",
		ClientSecret: "my-secret",
		RedirectURI:  "/auth/callback",
	})

	// 1. Generate authorization redirect URL
	authURL := client.GetAuthURL("random-state", "")
	log.Printf("Login at: %s", authURL)

	// 2. Exchange code for user profile
	user, err := client.ExchangeCode(context.Background(), "auth-code", "")
	if err != nil {
		log.Fatalf("Login error: %v", err)
	}
	log.Printf("Welcome, %s (%s)", user.DisplayName, user.Email)
}
```

### 4. Auto Update & Release CDN Integration (`updater`)

```go
package main

import (
	"log"

	"github.com/svipwly/fate-sdk-go/updater"
)

func checkUpdate() {
	manifestURL := "https://cdn.example.com/releases/myapp/latest.json"

	info, err := updater.CheckUpdate(manifestURL, "v0.1.0", "dev", "")
	if err != nil {
		log.Printf("Check update error: %v", err)
		return
	}

	if info.CanUpdate {
		log.Printf("Update available: %s -> %s", info.CurrentVersion, info.LatestVersion)
	}
}
```

---

## Packages

| Package | Import Path | Description |
| :--- | :--- | :--- |
| **`boot`** | `github.com/svipwly/fate-sdk-go/boot` | Startup log formatting, buildinfo reflection, version CLI |
| **`updater`** | `github.com/svipwly/fate-sdk-go/updater` | Release manifest parser, SemVer comparator, atomic binary self-upgrader |
| **`ginspa`** | `github.com/svipwly/fate-sdk-go/ginspa` | Embedded frontend SPA static server for Gin with caching and routing fallback |
| **`oidc`** | `github.com/svipwly/fate-sdk-go/oidc` | OpenID Connect / OAuth 2.0 client, token cache, and session manager |

---

## License

[MIT License](LICENSE) © 2026 Fate Authors
