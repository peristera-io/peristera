package pii

import (
	"context"
	"errors"
	"sync"
)

// InMemoryPseudonymStore is a PseudonymStore for tests, small deployments,
// and as the reference implementation of the interface contract (real
// tenants use a Postgres store). It enforces one token per subject, so the
// allocation-race and single-Delete-erases guarantees hold.
type InMemoryPseudonymStore struct {
	mu      sync.Mutex
	byToken map[string]Subject
	bySub   map[string]string // subject.String() → token
}

// NewInMemoryPseudonymStore returns an empty store.
func NewInMemoryPseudonymStore() *InMemoryPseudonymStore {
	return &InMemoryPseudonymStore{byToken: map[string]Subject{}, bySub: map[string]string{}}
}

// NewInMemoryPseudonyms wraps a fresh in-memory store in a Pseudonyms.
func NewInMemoryPseudonyms() *Pseudonyms {
	return NewPseudonyms(NewInMemoryPseudonymStore())
}

func (m *InMemoryPseudonymStore) Lookup(_ context.Context, s Subject) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tok, ok := m.bySub[s.String()]
	return tok, ok, nil
}

// Save rejects an already-mapped subject (the UNIQUE-on-subject contract).
func (m *InMemoryPseudonymStore) Save(_ context.Context, token string, s Subject) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.bySub[s.String()]; exists {
		return errors.New("pii: subject already mapped")
	}
	m.byToken[token] = s
	m.bySub[s.String()] = token
	return nil
}

func (m *InMemoryPseudonymStore) Resolve(_ context.Context, token string) (Subject, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.byToken[token]
	return s, ok, nil
}

func (m *InMemoryPseudonymStore) Delete(_ context.Context, s Subject) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tok, ok := m.bySub[s.String()]; ok {
		delete(m.byToken, tok)
		delete(m.bySub, s.String())
	}
	return nil
}
