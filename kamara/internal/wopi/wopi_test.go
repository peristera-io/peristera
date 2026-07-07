package wopi

import (
	"context"
	"testing"
	"time"

	"github.com/peristera-io/peristera/lib/pii"
)

// in-memory Store
type memStore struct{ m map[string]Session }

func newMemStore() *memStore { return &memStore{m: map[string]Session{}} }

func (s *memStore) Put(_ context.Context, h string, sess Session) error { s.m[h] = sess; return nil }
func (s *memStore) Get(_ context.Context, h string) (Session, bool, error) {
	sess, ok := s.m[h]
	return sess, ok, nil
}
func (s *memStore) DeleteExpired(_ context.Context, before time.Time) error {
	for h, sess := range s.m {
		if sess.Expires.Before(before) {
			delete(s.m, h)
		}
	}
	return nil
}
func (s *memStore) DeleteByObject(_ context.Context, objectID string) error {
	for h, sess := range s.m {
		if sess.ObjectID == objectID {
			delete(s.m, h)
		}
	}
	return nil
}

// fake authz whose answer we can flip to model a revoked share.
type fakeAuthz struct{ allow bool }

func (a *fakeAuthz) Check(_ context.Context, _ pii.Subject, _, _ string) (bool, error) {
	return a.allow, nil
}

func TestMintValidateRoundTrip(t *testing.T) {
	ctx := context.Background()
	az := &fakeAuthz{allow: true}
	s := NewSessions(newMemStore(), az, time.Hour)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}

	tok, err := s.Mint(ctx, alice, "file-1", true)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.Validate(ctx, tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if sess.ObjectID != "file-1" || sess.Subject != alice || !sess.CanWrite {
		t.Errorf("session = %+v", sess)
	}
}

func TestValidateRejectsUnknownToken(t *testing.T) {
	s := NewSessions(newMemStore(), &fakeAuthz{allow: true}, time.Hour)
	if _, err := s.Validate(context.Background(), "not-a-real-token"); err != ErrInvalid {
		t.Errorf("err = %v, want ErrInvalid", err)
	}
	if _, err := s.Validate(context.Background(), ""); err != ErrInvalid {
		t.Errorf("empty token err = %v, want ErrInvalid", err)
	}
}

func TestValidateRejectsExpired(t *testing.T) {
	ctx := context.Background()
	s := NewSessions(newMemStore(), &fakeAuthz{allow: true}, time.Hour)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	tok, _ := s.Mint(ctx, alice, "file-1", true)

	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) } // past TTL
	if _, err := s.Validate(ctx, tok); err != ErrInvalid {
		t.Errorf("expired err = %v, want ErrInvalid", err)
	}
}

// The token is not sufficient on its own: access is re-checked live, so a
// revoked share stops working immediately (ADR-0018).
func TestValidateRechecksAuthorization(t *testing.T) {
	ctx := context.Background()
	az := &fakeAuthz{allow: true}
	s := NewSessions(newMemStore(), az, time.Hour)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	tok, _ := s.Mint(ctx, alice, "file-1", true)

	az.allow = false // access revoked mid-session
	if _, err := s.Validate(ctx, tok); err != ErrInvalid {
		t.Errorf("revoked-access err = %v, want ErrInvalid", err)
	}
}

func TestRevokeDropsSessions(t *testing.T) {
	ctx := context.Background()
	s := NewSessions(newMemStore(), &fakeAuthz{allow: true}, time.Hour)
	alice := pii.Subject{Instance: "demo.example", UserID: "alice"}
	tok, _ := s.Mint(ctx, alice, "file-1", true)

	if err := s.Revoke(ctx, "file-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Validate(ctx, tok); err != ErrInvalid {
		t.Errorf("after revoke err = %v, want ErrInvalid", err)
	}
}
