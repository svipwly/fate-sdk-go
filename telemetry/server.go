package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HubOptions configures the central telemetry server Hub.
type HubOptions struct {
	Store            Store
	OfflineThreshold time.Duration // Default: 90s (3 missed heartbeats)
}

// Hub manages telemetry heartbeats, token validation, and command queuing.
type Hub struct {
	store            Store
	offlineThreshold time.Duration
}

// NewHub initializes a telemetry Hub with the given options.
func NewHub(opts HubOptions) *Hub {
	store := opts.Store
	if store == nil {
		store, _ = NewJSONFileStore("")
	}
	threshold := opts.OfflineThreshold
	if threshold <= 0 {
		threshold = 90 * time.Second
	}
	return &Hub{
		store:            store,
		offlineThreshold: threshold,
	}
}

// GetStore exposes the underlying store.
func (h *Hub) GetStore() Store {
	return h.store
}

// AddToken generates and registers a new unbound token.
func (h *Hub) AddToken() (*TokenRecord, error) {
	return h.store.AddToken("")
}

// ListTokens returns all tokens stored in the system.
func (h *Hub) ListTokens() ([]*TokenRecord, error) {
	return h.store.ListTokens()
}

// RevokeToken marks a token as revoked.
func (h *Hub) RevokeToken(token string) error {
	return h.store.RevokeToken(token)
}

// GetInstances returns instances optionally filtered by app, computing current online/offline status.
func (h *Hub) GetInstances(appFilter string) ([]*InstanceInfo, error) {
	instances, err := h.store.ListInstances(appFilter)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	for _, inst := range instances {
		if now.Sub(inst.LastSeenAt) <= h.offlineThreshold {
			inst.Status = InstanceStatusOnline
		} else {
			inst.Status = InstanceStatusOffline
		}
	}
	return instances, nil
}

// DispatchUpgrade queues an upgrade command for an entire app or a specific instance (format: "app" or "app:instance").
func (h *Hub) DispatchUpgrade(target string, spec UpgradeSpec) (*Command, error) {
	parts := strings.SplitN(strings.TrimSpace(target), ":", 2)
	targetApp := parts[0]
	targetInstance := ""
	if len(parts) > 1 {
		targetInstance = parts[1]
	}

	if targetApp == "" {
		return nil, fmt.Errorf("target app cannot be empty")
	}

	randomID := make([]byte, 8)
	_, _ = rand.Read(randomID)
	cmd := &Command{
		ID:        fmt.Sprintf("cmd_upg_%s", hex.EncodeToString(randomID)),
		Type:      "UPGRADE",
		CreatedAt: time.Now(),
		Upgrade:   &spec,
	}

	if err := h.store.QueueCommand(targetApp, targetInstance, cmd); err != nil {
		return nil, fmt.Errorf("queue upgrade command failed: %w", err)
	}

	return cmd, nil
}

// RegisterHTTPHandlers registers the heartbeat endpoint onto a standard library http.ServeMux.
func (h *Hub) RegisterHTTPHandlers(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/telemetry/heartbeat", h.HandleHeartbeatHTTP)
}

// HandleHeartbeatHTTP handles incoming client heartbeat requests.
func (h *Hub) HandleHeartbeatHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Authenticate Bearer token
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{
			Ack:     false,
			Message: "missing Authorization header",
		})
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{
			Ack:     false,
			Message: "invalid Bearer token format",
		})
		return
	}

	rec, err := h.store.GetToken(token)
	if err != nil || rec.Status == TokenStatusRevoked {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{
			Ack:     false,
			Message: "invalid or revoked token",
		})
		return
	}

	// 2. Decode payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{
			Ack:     false,
			Message: "failed to read request body",
		})
		return
	}

	var payload HeartbeatPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(HeartbeatResponse{
			Ack:     false,
			Message: "malformed JSON payload",
		})
		return
	}

	appName := strings.TrimSpace(payload.AppName)
	if appName == "" {
		appName = "unknown"
	}

	instanceName := strings.TrimSpace(payload.InstanceName)
	if instanceName == "" {
		instanceName = strings.TrimSpace(payload.Hostname)
	}
	if instanceName == "" {
		instanceName = "default"
	}

	// 3. Auto-bind token on first heartbeat if currently unbound
	if rec.Status == TokenStatusUnbound || rec.AppName == "" {
		_, _ = h.store.BindToken(token, appName, instanceName)
	}

	// 4. Update instance runtime state
	remoteIP := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		remoteIP = strings.Split(forwarded, ",")[0]
	} else if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		remoteIP = realIP
	}

	now := time.Now()
	instInfo := &InstanceInfo{
		Token:        token,
		AppName:      appName,
		InstanceName: instanceName,
		Hostname:     payload.Hostname,
		OS:           payload.OS,
		Arch:         payload.Arch,
		Version:      payload.Version,
		Commit:       payload.Commit,
		UptimeSec:    payload.UptimeSec,
		Goroutines:   payload.Goroutines,
		MemAllocMB:   payload.MemAllocMB,
		RemoteIP:     remoteIP,
		Status:       InstanceStatusOnline,
		LastSeenAt:   now,
		CustomMeta:   payload.CustomMeta,
		LastTaskAck:  payload.LastTaskAck,
	}

	_ = h.store.UpsertInstance(instInfo)

	// 5. Check if there is any pending command queued for this instance
	cmd, _ := h.store.PopCommand(appName, instanceName)

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(HeartbeatResponse{
		Ack:        true,
		ServerTime: now.Unix(),
		Command:    cmd,
	})
}
