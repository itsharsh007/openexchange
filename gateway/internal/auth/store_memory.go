package auth

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory UserStore for tests and local runs without Postgres.
// Safe for concurrent use.
type MemoryStore struct {
	mu     sync.RWMutex
	byMail map[string]User
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byMail: make(map[string]User)}
}

// Create inserts u, keyed by its normalized email.
func (m *MemoryStore) Create(_ context.Context, u User) error {
	key := NormalizeEmail(u.Email)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byMail[key]; ok {
		return ErrEmailTaken
	}
	u.Email = key
	m.byMail[key] = u
	return nil
}

// ByEmail returns the user with the given email, or ErrNoUser.
func (m *MemoryStore) ByEmail(_ context.Context, email string) (User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.byMail[NormalizeEmail(email)]
	if !ok {
		return User{}, ErrNoUser
	}
	return u, nil
}
