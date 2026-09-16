package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOIDCClient(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "mock-access-token",
			"id_token":     "mock-id-token",
		})
	})

	mux.HandleFunc("/oauth2/userinfo", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mock-access-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(User{
			ID:          "user-123",
			Username:    "admin",
			DisplayName: "Administrator",
			Email:       "admin@example.com",
			Role:        "admin",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := NewClient(Config{
		IssuerURL:    server.URL,
		ClientID:     "test-app",
		ClientSecret: "test-secret",
		RedirectURI:  "/callback",
		SessionTTL:   time.Hour,
	})

	// 1. Test ExchangeCode
	user, err := client.ExchangeCode(context.Background(), "auth-code", "/callback")
	if err != nil {
		t.Fatalf("ExchangeCode failed: %v", err)
	}
	if user.Username != "admin" {
		t.Errorf("expected username admin, got %s", user.Username)
	}

	// 2. Test Session Management
	sessionID := client.CreateSession(*user)
	if sessionID == "" {
		t.Fatalf("expected non-empty session ID")
	}

	sessionUser, ok := client.GetSession(sessionID)
	if !ok || sessionUser.ID != "user-123" {
		t.Errorf("GetSession failed or returned invalid user")
	}

	client.DeleteSession(sessionID)
	if _, ok := client.GetSession(sessionID); ok {
		t.Errorf("expected session to be deleted")
	}

	// 3. Test State Generation & Verification
	state, err := client.GenerateState()
	if err != nil {
		t.Fatalf("GenerateState failed: %v", err)
	}
	if !client.VerifyState(state) {
		t.Errorf("expected VerifyState to succeed")
	}
	if client.VerifyState(state) {
		t.Errorf("expected duplicate VerifyState to fail")
	}
}
