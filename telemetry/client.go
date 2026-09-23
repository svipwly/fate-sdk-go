package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/svipwly/fate-sdk-go/boot"
	"github.com/svipwly/fate-sdk-go/updater"
)

// AppInfo provides identity metadata for environment-based initialization.
type AppInfo struct {
	AppName       string
	Version       string
	Commit        string
	ManifestURL   string
	MetaCollector func() map[string]any
}

// ClientConfig configures the telemetry background reporter.
type ClientConfig struct {
	ServerURL      string
	Token          string
	AppName        string
	InstanceName   string // Optional: defaults to os.Hostname()
	Version        string
	Commit         string
	ManifestURL    string
	ReportInterval time.Duration
	MetaCollector  func() map[string]any
	Logger         func(format string, args ...any)
}

// StartReporterFromEnv inspects environment variables (ARKS_SERVER_URL, ARKS_INSTANCE_TOKEN).
// If configured, it starts the background reporter and returns true.
// If missing, it silently skips and returns false, without affecting host service startup.
func StartReporterFromEnv(ctx context.Context, app AppInfo) bool {
	serverURL := strings.TrimSpace(os.Getenv("ARKS_SERVER_URL"))
	token := strings.TrimSpace(os.Getenv("ARKS_INSTANCE_TOKEN"))

	if serverURL == "" || token == "" {
		return false
	}

	instanceName := strings.TrimSpace(os.Getenv("ARKS_INSTANCE_NAME"))
	interval := 30 * time.Second
	if intervalStr := os.Getenv("ARKS_REPORT_INTERVAL"); intervalStr != "" {
		if d, err := time.ParseDuration(intervalStr); err == nil && d >= 5*time.Second {
			interval = d
		}
	}

	manifestURL := app.ManifestURL
	if manifestURL == "" {
		manifestURL = os.Getenv("MANIFEST_URL")
	}

	cfg := ClientConfig{
		ServerURL:      serverURL,
		Token:          token,
		AppName:        app.AppName,
		InstanceName:   instanceName,
		Version:        app.Version,
		Commit:         app.Commit,
		ManifestURL:    manifestURL,
		ReportInterval: interval,
		MetaCollector:  app.MetaCollector,
	}

	StartReporter(ctx, cfg)
	return true
}

// StartReporter starts the background telemetry heartbeat loop.
func StartReporter(ctx context.Context, cfg ClientConfig) {
	if strings.TrimSpace(cfg.ServerURL) == "" || strings.TrimSpace(cfg.Token) == "" {
		return
	}

	if cfg.ReportInterval <= 0 {
		cfg.ReportInterval = 30 * time.Second
	}

	if cfg.InstanceName == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			cfg.InstanceName = h
		} else {
			cfg.InstanceName = "unknown-host"
		}
	}

	cfg.Commit = boot.ResolveCommit(cfg.Commit)

	r := &reporter{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		startTime:  time.Now(),
	}

	go r.run(ctx)
}

type reporter struct {
	cfg         ClientConfig
	httpClient  *http.Client
	startTime   time.Time
	lastTaskAck *TaskAck
	ackMu       sync.Mutex
}

func (r *reporter) log(format string, args ...any) {
	if r.cfg.Logger != nil {
		r.cfg.Logger(format, args...)
	} else {
		log.Printf("[Telemetry] "+format, args...)
	}
}

func (r *reporter) run(ctx context.Context) {
	// First immediate heartbeat upon startup
	r.sendHeartbeat()

	ticker := time.NewTicker(r.cfg.ReportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sendHeartbeat()
		}
	}
}

func (r *reporter) sendHeartbeat() {
	var mStats runtime.MemStats
	runtime.ReadMemStats(&mStats)

	var customMeta map[string]any
	if r.cfg.MetaCollector != nil {
		customMeta = r.cfg.MetaCollector()
	}

	r.ackMu.Lock()
	currentAck := r.lastTaskAck
	r.ackMu.Unlock()

	hostname, _ := os.Hostname()
	payload := HeartbeatPayload{
		AppName:      r.cfg.AppName,
		InstanceName: r.cfg.InstanceName,
		Hostname:     hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Version:      r.cfg.Version,
		Commit:       r.cfg.Commit,
		UptimeSec:    int64(time.Since(r.startTime).Seconds()),
		Goroutines:   runtime.NumGoroutine(),
		MemAllocMB:   float64(mStats.Alloc) / (1024 * 1024),
		CustomMeta:   customMeta,
		LastTaskAck:  currentAck,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	endpoint := fmt.Sprintf("%s/api/v1/telemetry/heartbeat", strings.TrimRight(r.cfg.ServerURL, "/"))
	req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.cfg.Token))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		// Network unreachable / transient failure: silently back off
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	// Ack sent successfully, clear pending ack
	if currentAck != nil {
		r.ackMu.Lock()
		if r.lastTaskAck == currentAck {
			r.lastTaskAck = nil
		}
		r.ackMu.Unlock()
	}

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var hbResp HeartbeatResponse
	if err := json.Unmarshal(rawResp, &hbResp); err != nil {
		return
	}

	// Handle piggybacked command
	if hbResp.Command != nil {
		go r.handleCommand(hbResp.Command)
	}
}

func (r *reporter) handleCommand(cmd *Command) {
	switch cmd.Type {
	case "UPGRADE":
		if cmd.Upgrade == nil {
			r.recordAck(cmd.ID, "FAILED", "missing upgrade specification")
			return
		}

		r.log("Received UPGRADE command: target=%s, url=%s", cmd.Upgrade.TargetVersion, cmd.Upgrade.DownloadURL)

		req := updater.InProcessUpgradeReq{
			TargetVersion: cmd.Upgrade.TargetVersion,
			DownloadURL:   cmd.Upgrade.DownloadURL,
			SHA256:        cmd.Upgrade.SHA256,
			Force:         cmd.Upgrade.Force,
			RestartDelay:  1 * time.Second,
		}

		err := updater.PerformInProcessUpgrade(r.cfg.ManifestURL, r.cfg.Version, req)
		if err != nil {
			r.log("In-process upgrade failed: %v", err)
			r.recordAck(cmd.ID, "FAILED", err.Error())
		} else {
			r.log("In-process upgrade succeeded! Restart scheduled.")
			r.recordAck(cmd.ID, "SUCCESS", "")
			// Try to send one last quick heartbeat ack before process restarts
			time.Sleep(200 * time.Millisecond)
			r.sendHeartbeat()
		}

	default:
		r.recordAck(cmd.ID, "FAILED", fmt.Sprintf("unsupported command type: %s", cmd.Type))
	}
}

func (r *reporter) recordAck(commandID, status, errMsg string) {
	r.ackMu.Lock()
	defer r.ackMu.Unlock()
	r.lastTaskAck = &TaskAck{
		CommandID:  commandID,
		Status:     status,
		ErrorMsg:   errMsg,
		ExecutedAt: time.Now(),
	}
}
