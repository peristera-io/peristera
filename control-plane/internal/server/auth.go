package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/session"
)

// tokenCache remembers recently validated bearer tokens so every API call
// doesn't hit the userinfo endpoint. Backed by lib/session so it evicts
// (closing the unbounded-growth half of issue #3). Browser sessions are
// handled by the relying party's cookie; this is the API path.
type tokenCache struct {
	store *session.Store[bool]
}

func newTokenCache(ttl time.Duration) *tokenCache {
	return &tokenCache{store: session.NewStore[bool](ttl)}
}

func (t *tokenCache) valid(tok string) bool {
	_, ok := t.store.Get(tok)
	return ok
}

func (t *tokenCache) remember(tok string) {
	t.store.Put(tok, true)
}

// requireAuth guards a handler: a browser session cookie (via the relying
// party) or a bearer token (validated against the default instance's
// userinfo endpoint) both pass. API callers get JSON 401; browsers get the
// login redirect.
func (s *Server) requireAuth(next http.Handler, isAPI bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.rp.Session(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); len(auth) > 7 && auth[:7] == "Bearer " {
			tok := auth[7:]
			if s.tokens.valid(tok) || s.IAM.UserinfoOK(r.Context(), s.Cfg.Issuer, tok) {
				s.tokens.remember(tok)
				next.ServeHTTP(w, r)
				return
			}
		}
		if isAPI {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "authentication required"})
			return
		}
		http.Redirect(w, r, "/auth/login", http.StatusFound)
	})
}

// user returns the current browser session, if any.
func (s *Server) user(r *http.Request) (oidcrp.Session, bool) {
	return s.rp.Session(r)
}
