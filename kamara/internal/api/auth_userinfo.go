package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/session"
)

// UserinfoAuth resolves a bearer token to a subject by calling the tenant
// OIDC provider's userinfo endpoint (the same cheap validation the control
// plane uses, ADR-0013), and extracting the `sub` claim as the user id.
// The subject's instance is the tenant's permanent domain — the issuer's
// host (ADR-0009 §2) — so a file's owner is stable across token rotations.
//
// Validated tokens are cached for a short TTL (backed by lib/session, so it
// evicts) to keep every API call from hitting userinfo. The cache holds the
// resolved subject, not just validity, because the handlers need the owner.
type UserinfoAuth struct {
	issuer   string
	instance string
	http     *http.Client
	cache    *session.Store[pii.Subject]
}

// NewUserinfoAuth builds an authenticator for one issuer. instance is the
// tenant domain recorded as the subject's instance (typically the issuer's
// host).
func NewUserinfoAuth(issuer, instance string, ttl time.Duration) *UserinfoAuth {
	if ttl == 0 {
		ttl = 60 * time.Second
	}
	return &UserinfoAuth{
		issuer:   issuer,
		instance: instance,
		http:     &http.Client{Timeout: 5 * time.Second},
		cache:    session.NewStore[pii.Subject](ttl),
	}
}

// Subject validates the token and returns the caller's subject. ok is false
// for an invalid/expired token; err is set only on a transport/provider
// failure (so the caller can distinguish "denied" from "provider down").
func (a *UserinfoAuth) Subject(ctx context.Context, token string) (pii.Subject, bool, error) {
	if s, ok := a.cache.Get(token); ok {
		return s, true, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.issuer+"/oidc/v1/userinfo", nil)
	if err != nil {
		return pii.Subject{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := a.http.Do(req)
	if err != nil {
		return pii.Subject{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return pii.Subject{}, false, nil // a genuine "not a valid token"
	}
	if resp.StatusCode != http.StatusOK {
		return pii.Subject{}, false, fmt.Errorf("userinfo: unexpected status %d", resp.StatusCode)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claims); err != nil {
		return pii.Subject{}, false, fmt.Errorf("userinfo: decode: %w", err)
	}
	if claims.Sub == "" {
		return pii.Subject{}, false, fmt.Errorf("userinfo: no sub claim")
	}
	s := pii.Subject{Instance: a.instance, UserID: claims.Sub}
	a.cache.Put(token, s)
	return s, true, nil
}

var _ Authenticator = (*UserinfoAuth)(nil)
