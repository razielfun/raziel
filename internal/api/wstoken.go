package api

import (
	"sync"
	"time"
)

// wsTokenStore is a thread-safe in-memory store for short-lived WebSocket tokens.
// Tokens expire after 60 seconds and are single-use.
type wsTokenStore struct {
	mu     sync.Mutex
	tokens map[string]wsTokenEntry
}

type wsTokenEntry struct {
	sandboxID string
	expiresAt time.Time
	used      bool
}

func newWsTokenStore() *wsTokenStore {
	s := &wsTokenStore{tokens: make(map[string]wsTokenEntry)}
	go s.sweep()
	return s
}

// Register stores a token associated with a sandbox. The token expires after 60 seconds.
func (s *wsTokenStore) Register(token, sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = wsTokenEntry{
		sandboxID: sandboxID,
		expiresAt: time.Now().Add(60 * time.Second),
	}
}

// Consume validates the token and, if valid, marks it used and returns the sandbox ID.
// Returns ("", false) if the token is unknown, expired, or already used.
func (s *wsTokenStore) Consume(token string) (sandboxID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, exists := s.tokens[token]
	if !exists || entry.used || time.Now().After(entry.expiresAt) {
		return "", false
	}
	entry.used = true
	s.tokens[token] = entry
	return entry.sandboxID, true
}

func (s *wsTokenStore) sweep() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for tok, e := range s.tokens {
			if now.After(e.expiresAt) {
				delete(s.tokens, tok)
			}
		}
		s.mu.Unlock()
	}
}
