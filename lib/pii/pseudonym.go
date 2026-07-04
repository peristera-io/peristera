package pii

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"strings"
)

// Pseudonyms implements the per-subject pseudonymization ADR-0009 §7 relies
// on: append-only referrers (the audit log, ADR-0011) store a stable
// per-subject *token*, never the raw subject ID, so a single person can be
// erased by dropping one mapping row while the tamper-evident rows stay
// intact. This is per-subject and distinct from the per-tenant key
// hierarchy (§6), which erases a whole tenant.
type Pseudonyms struct {
	store PseudonymStore
}

// PseudonymStore persists the token↔subject mapping. Implementations are
// per-tenant (each tenant's mapping lives in its own database).
type PseudonymStore interface {
	// Lookup returns the token for a subject if one exists.
	Lookup(ctx context.Context, s Subject) (token string, ok bool, err error)
	// Save records a new token→subject mapping. Implementations MUST
	// enforce at most one token per subject (a UNIQUE constraint on the
	// subject); a Save for an already-mapped subject may return an error,
	// which TokenFor handles as "lost the allocation race" by re-reading
	// the winner. This keeps linkability erasable by a single Delete.
	Save(ctx context.Context, token string, s Subject) error
	// Resolve returns the subject a token maps to (for the audit viewer).
	// Missing means erased (or never issued): ok is false.
	Resolve(ctx context.Context, token string) (s Subject, ok bool, err error)
	// Delete drops the subject's mapping — the erasure operation. Must
	// remove every token for the subject (there is at most one, given the
	// Save constraint). Idempotent.
	Delete(ctx context.Context, s Subject) error
}

// NewPseudonyms wraps a store.
func NewPseudonyms(store PseudonymStore) *Pseudonyms {
	return &Pseudonyms{store: store}
}

// newToken returns a random, URL-safe, unpadded token. Random (not derived
// from the subject) so a token leaks nothing about the person and cannot be
// reversed without the mapping.
func newToken() string {
	b := make([]byte, 15) // 120 bits
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// TokenFor returns the subject's stable token, allocating one on first
// use. Concurrent first-uses race to allocate; the store's one-token-per-
// subject constraint (see Save) means the loser's Save fails, and we
// re-read the winner — so every caller converges on the same token.
func (p *Pseudonyms) TokenFor(ctx context.Context, s Subject) (string, error) {
	if tok, ok, err := p.store.Lookup(ctx, s); err != nil {
		return "", err
	} else if ok {
		return tok, nil
	}
	tok := newToken()
	if err := p.store.Save(ctx, tok, s); err != nil {
		// Likely lost the race — a correct store rejected the duplicate
		// subject. Re-read the winner; only surface the error if there
		// genuinely is no mapping.
		if existing, ok, lookErr := p.store.Lookup(ctx, s); lookErr == nil && ok {
			return existing, nil
		}
		return "", err
	}
	return tok, nil
}

// Resolve returns the subject a token refers to; ok is false once the
// subject has been erased.
func (p *Pseudonyms) Resolve(ctx context.Context, token string) (Subject, bool, error) {
	return p.store.Resolve(ctx, token)
}

// Erase drops a subject's mapping, breaking linkability of every append-only
// reference to that person.
func (p *Pseudonyms) Erase(ctx context.Context, s Subject) error {
	return p.store.Delete(ctx, s)
}
