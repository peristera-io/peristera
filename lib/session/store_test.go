package session

import (
	"testing"
	"time"
)

func TestPutGetDelete(t *testing.T) {
	s := NewStore[string](time.Hour)
	s.Put("k", "v")
	if got, ok := s.Get("k"); !ok || got != "v" {
		t.Fatalf("Get = %q,%v", got, ok)
	}
	s.Delete("k")
	if _, ok := s.Get("k"); ok {
		t.Error("still present after Delete")
	}
	if _, ok := s.Get("missing"); ok {
		t.Error("missing key reported present")
	}
}

func TestExpiryAndSweep(t *testing.T) {
	s := NewStore[int](time.Minute)
	clock := time.Unix(0, 0)
	s.now = func() time.Time { return clock }

	s.Put("a", 1)
	// Abandoned entries that are never Got must still be swept, or login
	// state would accumulate forever (issue #3).
	for i := 0; i < 5; i++ {
		s.Put(string(rune('b'+i)), i)
	}
	if s.Len() != 6 {
		t.Fatalf("len = %d, want 6", s.Len())
	}

	// Advance past the TTL and trigger a sweep via a Put.
	clock = clock.Add(2 * time.Minute)
	s.Put("fresh", 99)
	if _, ok := s.Get("a"); ok {
		t.Error("expired entry still gettable")
	}
	if s.Len() != 1 {
		t.Errorf("after sweep len = %d, want 1 (only 'fresh')", s.Len())
	}
}

func TestGetLazilyEvictsExpired(t *testing.T) {
	s := NewStore[string](time.Minute)
	clock := time.Unix(0, 0)
	s.now = func() time.Time { return clock }
	s.Put("k", "v")
	clock = clock.Add(2 * time.Minute)
	if _, ok := s.Get("k"); ok {
		t.Error("expired entry returned by Get")
	}
	if s.Len() != 0 {
		t.Error("Get did not lazily evict the expired entry")
	}
}
