package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const stateTTL = 10 * time.Minute

// State holds in-flight Canvas (or other DS) OAuth authorization data.
type State struct {
	TenantID         uint64 `json:"tenant_id"`
	DataSourceID     string `json:"data_source_id"`
	RedirectURI      string `json:"redirect_uri"`
	FrontendRedirect string `json:"frontend_redirect"`
	BaseURL          string `json:"base_url"`
	ClientID         string `json:"client_id"`
}

// StateStore persists opaque OAuth state values (Redis preferred, memory fallback).
type StateStore struct {
	rdb *redis.Client
	mu  sync.Mutex
	mem map[string]memEntry
}

type memEntry struct {
	value     State
	expiresAt time.Time
}

// NewStateStore creates a state store. rdb may be nil.
func NewStateStore(rdb *redis.Client) *StateStore {
	s := &StateStore{rdb: rdb, mem: make(map[string]memEntry)}
	if rdb == nil {
		go s.gcLoop()
	}
	return s
}

func (s *StateStore) key(state string) string {
	ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	if ns != "" {
		return "weknora:ds_oauth_state:" + ns + ":" + state
	}
	return "weknora:ds_oauth_state:" + state
}

// Put stores state with a fixed TTL.
func (s *StateStore) Put(ctx context.Context, state string, value State) error {
	if s.rdb != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return s.rdb.Set(ctx, s.key(state), data, stateTTL).Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[state] = memEntry{value: value, expiresAt: time.Now().Add(stateTTL)}
	return nil
}

// Take retrieves and deletes a state (single-use).
func (s *StateStore) Take(ctx context.Context, state string) (State, error) {
	if s.rdb != nil {
		data, err := s.rdb.GetDel(ctx, s.key(state)).Bytes()
		if err != nil {
			if err == redis.Nil {
				return State{}, fmt.Errorf("oauth state not found or expired")
			}
			return State{}, err
		}
		var v State
		if err := json.Unmarshal(data, &v); err != nil {
			return State{}, err
		}
		return v, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.mem[state]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(s.mem, state)
		return State{}, fmt.Errorf("oauth state not found or expired")
	}
	delete(s.mem, state)
	return entry.value, nil
}

func (s *StateStore) gcLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		for k, v := range s.mem {
			if now.After(v.expiresAt) {
				delete(s.mem, k)
			}
		}
		s.mu.Unlock()
	}
}

// NewStateToken returns a cryptographically random opaque state string.
func NewStateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
