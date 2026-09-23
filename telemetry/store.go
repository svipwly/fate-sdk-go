package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GenerateSecureToken creates an opaque, high-entropy token prefixed with arks_tok_.
func GenerateSecureToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback timestamp mix
		return fmt.Sprintf("arks_tok_%d%x", time.Now().UnixNano(), os.Getpid())
	}
	return fmt.Sprintf("arks_tok_%s", hex.EncodeToString(bytes))
}

// Store defines persistence operations for tokens, instance health, and pending commands.
type Store interface {
	AddToken(token string) (*TokenRecord, error)
	ListTokens() ([]*TokenRecord, error)
	GetToken(token string) (*TokenRecord, error)
	RevokeToken(token string) error
	BindToken(token, appName, instanceName string) (*TokenRecord, error)

	UpsertInstance(info *InstanceInfo) error
	ListInstances(appFilter string) ([]*InstanceInfo, error)
	GetInstance(appName, instanceName string) (*InstanceInfo, error)

	QueueCommand(targetApp, targetInstance string, cmd *Command) error
	PopCommand(appName, instanceName string) (*Command, error)
}

type fileStoreData struct {
	Tokens    map[string]*TokenRecord `json:"tokens"`
	Instances map[string]*InstanceInfo `json:"instances"` // key: "appName/instanceName" or token
	// Commands queued per target. key: "appName" or "appName/instanceName"
	Commands  map[string][]*Command   `json:"commands"`
}

// JSONFileStore implements Store backed by an atomic JSON file.
type JSONFileStore struct {
	filePath string
	mu       sync.RWMutex
	data     fileStoreData
}

// DefaultStoreFilePath returns ~/.config/fate/arks_control.json.
func DefaultStoreFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "arks_control.json"
	}
	return filepath.Join(home, ".config", "fate", "arks_control.json")
}

// NewJSONFileStore initializes a JSON-backed store at the specified file path.
func NewJSONFileStore(filePath string) (*JSONFileStore, error) {
	if filePath == "" {
		filePath = DefaultStoreFilePath()
	}

	s := &JSONFileStore{
		filePath: filePath,
		data: fileStoreData{
			Tokens:    make(map[string]*TokenRecord),
			Instances: make(map[string]*InstanceInfo),
			Commands:  make(map[string][]*Command),
		},
	}

	if err := s.load(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *JSONFileStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create store dir failed: %w", err)
	}

	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// First run: empty state
			return nil
		}
		return fmt.Errorf("read store file failed: %w", err)
	}

	var d fileStoreData
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("parse store JSON failed: %w", err)
	}

	if d.Tokens == nil {
		d.Tokens = make(map[string]*TokenRecord)
	}
	if d.Instances == nil {
		d.Instances = make(map[string]*InstanceInfo)
	}
	if d.Commands == nil {
		d.Commands = make(map[string][]*Command)
	}

	s.data = d
	return nil
}

func (s *JSONFileStore) persistLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create store dir failed: %w", err)
	}

	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal store JSON failed: %w", err)
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", s.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, raw, 0600); err != nil {
		return fmt.Errorf("write temp store failed: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("atomic rename store failed: %w", err)
	}

	return nil
}

func (s *JSONFileStore) AddToken(token string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if token == "" {
		token = GenerateSecureToken()
	}

	if _, exists := s.data.Tokens[token]; exists {
		return nil, fmt.Errorf("token already exists")
	}

	rec := &TokenRecord{
		Token:     token,
		Status:    TokenStatusUnbound,
		CreatedAt: time.Now(),
	}

	s.data.Tokens[token] = rec
	if err := s.persistLocked(); err != nil {
		return nil, err
	}

	return rec, nil
}

func (s *JSONFileStore) ListTokens() ([]*TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*TokenRecord, 0, len(s.data.Tokens))
	for _, rec := range s.data.Tokens {
		cp := *rec
		list = append(list, &cp)
	}
	return list, nil
}

func (s *JSONFileStore) GetToken(token string) (*TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.data.Tokens[token]
	if !ok {
		return nil, fmt.Errorf("token not found")
	}
	cp := *rec
	return &cp, nil
}

func (s *JSONFileStore) RevokeToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.data.Tokens[token]
	if !ok {
		return fmt.Errorf("token not found")
	}

	rec.Status = TokenStatusRevoked
	return s.persistLocked()
}

func (s *JSONFileStore) BindToken(token, appName, instanceName string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.data.Tokens[token]
	if !ok {
		return nil, fmt.Errorf("token not found")
	}

	if rec.Status == TokenStatusRevoked {
		return nil, fmt.Errorf("cannot bind revoked token")
	}

	now := time.Now()
	rec.AppName = appName
	rec.InstanceName = instanceName
	rec.Status = TokenStatusActive
	if rec.BoundAt == nil {
		rec.BoundAt = &now
	}
	rec.LastSeenAt = &now

	if err := s.persistLocked(); err != nil {
		return nil, err
	}

	cp := *rec
	return &cp, nil
}

func (s *JSONFileStore) UpsertInstance(info *InstanceInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", strings.ToLower(info.AppName), strings.ToLower(info.InstanceName))
	existing, ok := s.data.Instances[key]
	if !ok {
		info.FirstSeenAt = time.Now()
		s.data.Instances[key] = info
	} else {
		// Retain FirstSeenAt
		info.FirstSeenAt = existing.FirstSeenAt
		s.data.Instances[key] = info
	}

	// Update token last seen as well
	if rec, ok := s.data.Tokens[info.Token]; ok {
		now := info.LastSeenAt
		rec.LastSeenAt = &now
	}

	return s.persistLocked()
}

func (s *JSONFileStore) ListInstances(appFilter string) ([]*InstanceInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter := strings.ToLower(strings.TrimSpace(appFilter))
	var res []*InstanceInfo
	for _, inst := range s.data.Instances {
		if filter == "" || strings.ToLower(inst.AppName) == filter {
			cp := *inst
			res = append(res, &cp)
		}
	}
	return res, nil
}

func (s *JSONFileStore) GetInstance(appName, instanceName string) (*InstanceInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", strings.ToLower(appName), strings.ToLower(instanceName))
	inst, ok := s.data.Instances[key]
	if !ok {
		return nil, fmt.Errorf("instance not found: %s", key)
	}
	cp := *inst
	return &cp, nil
}

func (s *JSONFileStore) QueueCommand(targetApp, targetInstance string, cmd *Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key string
	if targetInstance != "" {
		key = fmt.Sprintf("%s/%s", strings.ToLower(targetApp), strings.ToLower(targetInstance))
	} else {
		key = strings.ToLower(targetApp)
	}

	s.data.Commands[key] = append(s.data.Commands[key], cmd)
	return s.persistLocked()
}

func (s *JSONFileStore) PopCommand(appName, instanceName string) (*Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Check instance-specific queue first
	specificKey := fmt.Sprintf("%s/%s", strings.ToLower(appName), strings.ToLower(instanceName))
	if q, ok := s.data.Commands[specificKey]; ok && len(q) > 0 {
		cmd := q[0]
		s.data.Commands[specificKey] = q[1:]
		_ = s.persistLocked()
		return cmd, nil
	}

	// 2. Check app-wide broadcast queue
	appKey := strings.ToLower(appName)
	if q, ok := s.data.Commands[appKey]; ok && len(q) > 0 {
		cmd := q[0]
		// For broadcast commands, copy and return, keep queue for other instances
		// Or pop if one-time. For app-wide queue, pop if desired.
		s.data.Commands[appKey] = q[1:]
		_ = s.persistLocked()
		return cmd, nil
	}

	return nil, nil
}
