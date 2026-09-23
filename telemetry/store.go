package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// GenerateSecureToken creates an opaque, high-entropy token prefixed with tok_.
func GenerateSecureToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback timestamp mix
		return fmt.Sprintf("tok_%d%x", time.Now().UnixNano(), os.Getpid())
	}
	return fmt.Sprintf("tok_%s", hex.EncodeToString(bytes))
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

// MemoryStore implements Store using in-memory data structures (thread-safe, volatile).
type MemoryStore struct {
	mu        sync.RWMutex
	tokens    map[string]*TokenRecord
	instances map[string]*InstanceInfo
	commands  map[string][]*Command
}

// Ensure MemoryStore satisfies Store interface.
var _ Store = (*MemoryStore)(nil)

// NewMemoryStore initializes an in-memory telemetry store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tokens:    make(map[string]*TokenRecord),
		instances: make(map[string]*InstanceInfo),
		commands:  make(map[string][]*Command),
	}
}

func (s *MemoryStore) AddToken(token string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if token == "" {
		token = GenerateSecureToken()
	}

	if _, exists := s.tokens[token]; exists {
		return nil, fmt.Errorf("token already exists")
	}

	rec := &TokenRecord{
		Token:     token,
		Status:    TokenStatusUnbound,
		CreatedAt: time.Now(),
	}

	s.tokens[token] = rec
	return rec, nil
}

func (s *MemoryStore) ListTokens() ([]*TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*TokenRecord, 0, len(s.tokens))
	for _, rec := range s.tokens {
		cp := *rec
		list = append(list, &cp)
	}
	return list, nil
}

func (s *MemoryStore) GetToken(token string) (*TokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.tokens[token]
	if !ok {
		return nil, fmt.Errorf("token not found")
	}
	cp := *rec
	return &cp, nil
}

func (s *MemoryStore) RevokeToken(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.tokens[token]
	if !ok {
		return fmt.Errorf("token not found")
	}

	rec.Status = TokenStatusRevoked
	return nil
}

func (s *MemoryStore) BindToken(token, appName, instanceName string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.tokens[token]
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

	cp := *rec
	return &cp, nil
}

func (s *MemoryStore) UpsertInstance(info *InstanceInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("%s/%s", strings.ToLower(info.AppName), strings.ToLower(info.InstanceName))
	existing, ok := s.instances[key]
	if !ok {
		info.FirstSeenAt = time.Now()
		s.instances[key] = info
	} else {
		info.FirstSeenAt = existing.FirstSeenAt
		s.instances[key] = info
	}

	if rec, ok := s.tokens[info.Token]; ok {
		now := info.LastSeenAt
		rec.LastSeenAt = &now
	}

	return nil
}

func (s *MemoryStore) ListInstances(appFilter string) ([]*InstanceInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	filter := strings.ToLower(strings.TrimSpace(appFilter))
	var res []*InstanceInfo
	for _, inst := range s.instances {
		if filter == "" || strings.ToLower(inst.AppName) == filter {
			cp := *inst
			res = append(res, &cp)
		}
	}
	return res, nil
}

func (s *MemoryStore) GetInstance(appName, instanceName string) (*InstanceInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", strings.ToLower(appName), strings.ToLower(instanceName))
	inst, ok := s.instances[key]
	if !ok {
		return nil, fmt.Errorf("instance not found: %s", key)
	}
	cp := *inst
	return &cp, nil
}

func (s *MemoryStore) QueueCommand(targetApp, targetInstance string, cmd *Command) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var key string
	if targetInstance != "" {
		key = fmt.Sprintf("%s/%s", strings.ToLower(targetApp), strings.ToLower(targetInstance))
	} else {
		key = strings.ToLower(targetApp)
	}

	s.commands[key] = append(s.commands[key], cmd)
	return nil
}

func (s *MemoryStore) PopCommand(appName, instanceName string) (*Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	specificKey := fmt.Sprintf("%s/%s", strings.ToLower(appName), strings.ToLower(instanceName))
	if q, ok := s.commands[specificKey]; ok && len(q) > 0 {
		cmd := q[0]
		s.commands[specificKey] = q[1:]
		return cmd, nil
	}

	appKey := strings.ToLower(appName)
	if q, ok := s.commands[appKey]; ok && len(q) > 0 {
		cmd := q[0]
		s.commands[appKey] = q[1:]
		return cmd, nil
	}

	return nil, nil
}
