// Package session is an in-memory, TTL-evicting key→value store for HTTP
// session and short-lived login state. It bounds growth (expired entries
// are swept), which the M2 hand-rolled session maps did not — issue #3.
//
// In-memory means sessions do not survive a restart and do not share
// across replicas; that is the accepted convention for M3 single-replica
// apps (a shared/persistent store is a later decision).
package session

import (
	"sync"
	"time"
)

// Store maps opaque keys to values of type T, expiring each after a TTL.
type Store[T any] struct {
	ttl        time.Duration
	sweepEvery time.Duration
	mu         sync.Mutex
	items      map[string]entry[T]
	lastSweep  time.Time
	now        func() time.Time // injectable for tests
}

type entry[T any] struct {
	val T
	exp time.Time
}

// NewStore returns a store whose entries live for ttl.
func NewStore[T any](ttl time.Duration) *Store[T] {
	return &Store[T]{
		ttl:        ttl,
		sweepEvery: ttl, // sweep at most once per ttl window
		items:      map[string]entry[T]{},
		now:        time.Now,
	}
}

// Put stores v under key, (re)setting its expiry to now+ttl. It
// opportunistically sweeps expired entries so abandoned keys (e.g. login
// states never completed) cannot accumulate without bound.
func (s *Store[T]) Put(key string, v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.lastSweep) >= s.sweepEvery {
		s.sweepLocked(now)
	}
	s.items[key] = entry[T]{val: v, exp: now.Add(s.ttl)}
}

// Get returns the value for key if present and unexpired.
func (s *Store[T]) Get(key string) (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.items[key]
	if !ok || s.now().After(e.exp) {
		var zero T
		if ok {
			delete(s.items, key) // lazy eviction
		}
		return zero, false
	}
	return e.val, true
}

// Delete removes key (e.g. on logout or once a login state is consumed).
func (s *Store[T]) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
}

// Len reports the current entry count (including not-yet-swept expired
// ones); for tests/metrics.
func (s *Store[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

func (s *Store[T]) sweepLocked(now time.Time) {
	for k, e := range s.items {
		if now.After(e.exp) {
			delete(s.items, k)
		}
	}
	s.lastSweep = now
}
