// Package wopi implements Kamara's WOPI editing sessions — the security
// boundary for browser office editing (ADR-0018). When a user opens a file in
// the office engine (Collabora), Kamara mints an opaque, per-session access
// token scoped to (file, user, permission, TTL). Collabora presents it on
// every WOPI call. Collabora publishes no proof-key, so this token is the
// whole boundary: it is high-entropy, stored only as a SHA-256 hash, expires,
// and — critically — access is re-checked against OpenFGA on every call, so a
// revoked share stops working immediately rather than at TTL.
package wopi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/lib/pii"
)

// ErrInvalid is returned for a missing, unknown, expired, or no-longer-
// authorized token. The HTTP layer maps it to 401.
var ErrInvalid = errors.New("wopi: invalid or expired session")

// DefaultTTL bounds an editing session (a longer edit re-opens, minting
// anew). It is defense-in-depth, not the primary control: the per-call
// OpenFGA re-check is what actually gates access, so the TTL only bounds how
// long a *leaked* token could be replayed if that check ever regressed.
const DefaultTTL = 10 * time.Hour

// Session is a minted editing session (what a valid token resolves to).
type Session struct {
	ObjectID string
	Subject  pii.Subject
	CanWrite bool
	Expires  time.Time
}

// Store persists sessions keyed by the token's SHA-256 (never the token).
type Store interface {
	Put(ctx context.Context, tokenHash string, s Session) error
	Get(ctx context.Context, tokenHash string) (Session, bool, error)
	DeleteExpired(ctx context.Context, before time.Time) error
	DeleteByObject(ctx context.Context, objectID string) error
}

// Authorizer is the live access re-check (a subset of lib/authz).
type Authorizer interface {
	Check(ctx context.Context, user pii.Subject, relation, object string) (bool, error)
}

// Sessions mints and validates WOPI access tokens.
type Sessions struct {
	store Store
	authz Authorizer
	ttl   time.Duration
	now   func() time.Time
}

// NewSessions builds the service; ttl <= 0 uses DefaultTTL.
func NewSessions(store Store, authz Authorizer, ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Sessions{store: store, authz: authz, ttl: ttl, now: time.Now}
}

// Mint issues an opaque access token for editing objectID as subject. The
// caller (the /edit page) has already authorized the open; canWrite records
// whether this session may save back. The raw token is returned once; only its
// hash is stored.
func (s *Sessions) Mint(ctx context.Context, subject pii.Subject, objectID string, canWrite bool) (string, error) {
	raw := make([]byte, 32) // 256 bits — unguessable
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	sess := Session{ObjectID: objectID, Subject: subject, CanWrite: canWrite, Expires: now.Add(s.ttl)}
	if err := s.store.Put(ctx, hash(token), sess); err != nil {
		return "", err
	}
	_ = s.store.DeleteExpired(ctx, now) // opportunistic sweep; best-effort
	return token, nil
}

// Validate resolves a presented token to its session, enforcing both expiry
// and a live OpenFGA re-check. The token alone never suffices — the acting
// user must still have access to the file at call time.
func (s *Sessions) Validate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrInvalid
	}
	sess, ok, err := s.store.Get(ctx, hash(token))
	if err != nil {
		return Session{}, err
	}
	if !ok || s.now().After(sess.Expires) {
		return Session{}, ErrInvalid
	}
	ok, err = s.authz.Check(ctx, sess.Subject, file.AccessRelation, file.Type+":"+sess.ObjectID)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		return Session{}, ErrInvalid
	}
	return sess, nil
}

// Revoke drops every session for a file — an explicit, proactive
// invalidation. Note it is not required for correctness on delete: once a
// file's owner tuple is gone, Validate's per-call can_access re-check already
// fails, so the token stops resolving; Revoke additionally clears the stale
// rows before the TTL sweep. (Wiring it into file deletion is a fast-follow.)
func (s *Sessions) Revoke(ctx context.Context, objectID string) error {
	return s.store.DeleteByObject(ctx, objectID)
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
