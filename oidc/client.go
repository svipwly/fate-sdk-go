package oidc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// User represents authenticated user profile claims from the OIDC provider.
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatar_url"`
	Role        string `json:"role"`
}

// Session represents an active authenticated user session.
type Session struct {
	User      User
	ExpiresAt time.Time
}

type cachedToken struct {
	user      *User
	expiresAt time.Time
}

// Config defines connection parameters for the FateID OIDC provider.
type Config struct {
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	SessionTTL    time.Duration
	TokenCacheTTL time.Duration
	HTTPClient    *http.Client
}

// Client manages OIDC authentication flows, token exchanges, user profiles, and active sessions.
type Client struct {
	cfg        Config
	httpClient *http.Client
	sessions   sync.Map // sessionID -> Session
	tokenCache sync.Map // token -> cachedToken
	states     sync.Map // state -> expiresAt
}

// NewClient creates a new OIDC client instance.
func NewClient(cfg Config) *Client {
	if cfg.IssuerURL == "" {
		cfg.IssuerURL = os.Getenv("SSOID_ISSUER_URL")
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 7 * 24 * time.Hour
	}
	if cfg.TokenCacheTTL <= 0 {
		cfg.TokenCacheTTL = 5 * time.Minute
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &Client{
		cfg:        cfg,
		httpClient: httpClient,
	}
}

// GetAuthURL constructs the OAuth 2.0 authorization redirect URL.
func (c *Client) GetAuthURL(state, redirectURI string) string {
	if c.cfg.IssuerURL == "" {
		return ""
	}
	if redirectURI == "" {
		redirectURI = c.cfg.RedirectURI
	}
	authEndpoint := strings.TrimRight(c.cfg.IssuerURL, "/") + "/oauth2/authorize"

	params := url.Values{}
	params.Set("client_id", c.cfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "openid profile email")
	params.Set("state", state)

	return authEndpoint + "?" + params.Encode()
}

// ExchangeCode exchanges an authorization code for access token and fetches user profile.
func (c *Client) ExchangeCode(ctx context.Context, code, redirectURI string) (*User, error) {
	if c.cfg.IssuerURL == "" {
		return nil, fmt.Errorf("oidc: issuer URL is not configured")
	}
	if redirectURI == "" {
		redirectURI = c.cfg.RedirectURI
	}
	tokenEndpoint := strings.TrimRight(c.cfg.IssuerURL, "/") + "/oauth2/token"

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decode token response failed: %w", err)
	}

	return c.FetchUserInfo(ctx, tokenResp.AccessToken)
}

// FetchUserInfo retrieves user profile details using an OAuth 2.0 Access Token.
func (c *Client) FetchUserInfo(ctx context.Context, accessToken string) (*User, error) {
	userInfoEndpoint := strings.TrimRight(c.cfg.IssuerURL, "/") + "/oauth2/userinfo"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create userinfo request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var user User
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode user profile failed: %w", err)
	}

	return &user, nil
}

// ValidateBearerToken validates a Bearer API token with local in-memory caching.
func (c *Client) ValidateBearerToken(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}

	// Check local memory cache
	if val, ok := c.tokenCache.Load(token); ok {
		cached := val.(cachedToken)
		if time.Now().Before(cached.expiresAt) {
			return cached.user, nil
		}
		c.tokenCache.Delete(token)
	}

	user, err := c.FetchUserInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	c.tokenCache.Store(token, cachedToken{
		user:      user,
		expiresAt: time.Now().Add(c.cfg.TokenCacheTTL),
	})

	return user, nil
}

// SetSession stores a user session with an explicit session ID and custom TTL.
func (c *Client) SetSession(sessionID string, user User, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.cfg.SessionTTL
	}
	c.sessions.Store(sessionID, Session{
		User:      user,
		ExpiresAt: time.Now().Add(ttl),
	})
}

// CreateSession generates a secure session ID and stores user claims.
func (c *Client) CreateSession(user User) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	sessionID := hex.EncodeToString(b)

	c.sessions.Store(sessionID, Session{
		User:      user,
		ExpiresAt: time.Now().Add(c.cfg.SessionTTL),
	})
	return sessionID
}

// GetSession retrieves active user profile by session ID.
func (c *Client) GetSession(sessionID string) (*User, bool) {
	val, ok := c.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	session := val.(Session)
	if time.Now().After(session.ExpiresAt) {
		c.sessions.Delete(sessionID)
		return nil, false
	}
	return &session.User, true
}

// DeleteSession invalidates an active session ID.
func (c *Client) DeleteSession(sessionID string) {
	c.sessions.Delete(sessionID)
}

// GenerateState generates and tracks a random state token for CSRF protection.
func (c *Client) GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := hex.EncodeToString(b)
	c.states.Store(state, time.Now().Add(10*time.Minute))
	return state, nil
}

// VerifyState validates and consumes an OAuth state token.
func (c *Client) VerifyState(state string) bool {
	if state == "" {
		return false
	}
	val, ok := c.states.LoadAndDelete(state)
	if !ok {
		return false
	}
	expiresAt := val.(time.Time)
	return time.Now().Before(expiresAt)
}
