package telemetry

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenLifecycleAndStore(t *testing.T) {
	store := NewMemoryStore()

	// 1. Add token
	rec, err := store.AddToken("")
	if err != nil {
		t.Fatalf("add token failed: %v", err)
	}
	if rec.Token == "" || rec.Status != TokenStatusUnbound {
		t.Errorf("expected unbound token, got: %+v", rec)
	}

	// 2. List tokens
	list, err := store.ListTokens()
	if err != nil || len(list) != 1 {
		t.Fatalf("list tokens failed: len=%d, err=%v", len(list), err)
	}

	// 3. Bind token
	bound, err := store.BindToken(rec.Token, "ssoid", "ssoid-test-01")
	if err != nil {
		t.Fatalf("bind token failed: %v", err)
	}
	if bound.Status != TokenStatusActive || bound.AppName != "ssoid" || bound.InstanceName != "ssoid-test-01" {
		t.Errorf("unexpected bound record: %+v", bound)
	}

	// 4. Get token
	loaded, err := store.GetToken(rec.Token)
	if err != nil {
		t.Fatalf("get token failed: %v", err)
	}
	if loaded.AppName != "ssoid" || loaded.Status != TokenStatusActive {
		t.Errorf("loaded token mismatch: %+v", loaded)
	}

	// 5. Revoke token
	if err := store.RevokeToken(rec.Token); err != nil {
		t.Fatalf("revoke token failed: %v", err)
	}
	revoked, _ := store.GetToken(rec.Token)
	if revoked.Status != TokenStatusRevoked {
		t.Errorf("expected revoked status, got: %s", revoked.Status)
	}
}

func TestHeartbeatAutoBindingAndAck(t *testing.T) {
	store := NewMemoryStore()
	hub := NewHub(HubOptions{
		Store:            store,
		OfflineThreshold: 2 * time.Second,
	})

	mux := http.NewServeMux()
	hub.RegisterHTTPHandlers(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Generate token on server
	rec, err := hub.AddToken()
	if err != nil {
		t.Fatalf("add token: %v", err)
	}

	// 2. Client sends heartbeat with unauthenticated request -> expect 401
	unauthReq, _ := http.NewRequest("POST", ts.URL+"/api/v1/telemetry/heartbeat", bytes.NewBuffer([]byte("{}")))
	resp, err := http.DefaultClient.Do(unauthReq)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got: %v, err=%v", resp.StatusCode, err)
	}

	// 3. Client sends heartbeat with valid token -> auto-binds app and instance
	payload := HeartbeatPayload{
		AppName:      "pico",
		InstanceName: "pico-node-hk",
		Hostname:     "hk-vps",
		OS:           "linux",
		Arch:         "amd64",
		Version:      "v0.1.2",
		Commit:       "abcdef",
		UptimeSec:    120,
		Goroutines:   15,
		MemAllocMB:   10.5,
	}
	payloadBytes, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/telemetry/heartbeat", bytes.NewBuffer(payloadBytes))
	req.Header.Set("Authorization", "Bearer "+rec.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %v, err=%v", resp.StatusCode, err)
	}

	var hbResp HeartbeatResponse
	if err := json.NewDecoder(resp.Body).Decode(&hbResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !hbResp.Ack {
		t.Errorf("expected ack=true")
	}

	// Verify server store has auto-bound the token
	updatedToken, _ := store.GetToken(rec.Token)
	if updatedToken.Status != TokenStatusActive || updatedToken.AppName != "pico" || updatedToken.InstanceName != "pico-node-hk" {
		t.Errorf("expected token auto-bound to pico, got: %+v", updatedToken)
	}

	// Verify instances list
	instances, err := hub.GetInstances("pico")
	if err != nil || len(instances) != 1 {
		t.Fatalf("expected 1 instance, got: %d (err=%v)", len(instances), err)
	}
	if instances[0].Status != InstanceStatusOnline || instances[0].Version != "v0.1.2" {
		t.Errorf("unexpected instance data: %+v", instances[0])
	}
}

func TestCommandPiggybacking(t *testing.T) {
	hub := NewHub(HubOptions{}) // Default to MemoryStore

	mux := http.NewServeMux()
	hub.RegisterHTTPHandlers(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	rec, _ := hub.AddToken()

	// Initial heartbeat to register instance
	initPayload, _ := json.Marshal(HeartbeatPayload{
		AppName:      "ssoid",
		InstanceName: "node-1",
		Version:      "v1.0.0",
	})
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/telemetry/heartbeat", bytes.NewBuffer(initPayload))
	req.Header.Set("Authorization", "Bearer "+rec.Token)
	_, _ = http.DefaultClient.Do(req)

	// Queue an upgrade command
	cmd, err := hub.DispatchUpgrade("ssoid:node-1", UpgradeSpec{
		TargetVersion: "v1.0.1",
		DownloadURL:   "https://example.com/ssoid_v1.0.1",
		SHA256:        "mockhash",
	})
	if err != nil {
		t.Fatalf("dispatch upgrade: %v", err)
	}

	// Next heartbeat should piggyback the command
	req2, _ := http.NewRequest("POST", ts.URL+"/api/v1/telemetry/heartbeat", bytes.NewBuffer(initPayload))
	req2.Header.Set("Authorization", "Bearer "+rec.Token)
	resp, err := http.DefaultClient.Do(req2)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("heartbeat failed: %v", err)
	}

	var hbResp HeartbeatResponse
	_ = json.NewDecoder(resp.Body).Decode(&hbResp)
	if hbResp.Command == nil || hbResp.Command.ID != cmd.ID {
		t.Fatalf("expected command %s, got: %+v", cmd.ID, hbResp.Command)
	}
	if hbResp.Command.Upgrade.TargetVersion != "v1.0.1" {
		t.Errorf("unexpected target version: %s", hbResp.Command.Upgrade.TargetVersion)
	}

	// Next heartbeat should have no pending command
	req3, _ := http.NewRequest("POST", ts.URL+"/api/v1/telemetry/heartbeat", bytes.NewBuffer(initPayload))
	req3.Header.Set("Authorization", "Bearer "+rec.Token)
	resp3, _ := http.DefaultClient.Do(req3)
	var hbResp3 HeartbeatResponse
	_ = json.NewDecoder(resp3.Body).Decode(&hbResp3)
	if hbResp3.Command != nil {
		t.Errorf("expected no command, got: %+v", hbResp3.Command)
	}
}
