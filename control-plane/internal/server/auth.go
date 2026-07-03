package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// In-memory sessions — accepted M2 limitation (restart logs everyone
// out); the shared session convention gets an ADR before M3.
type sessionStore struct {
	mu sync.Mutex
	m  map[string]sessionData
	// login states → PKCE verifier
	logins map[string]string
}

type sessionData struct {
	Name, Email, IDToken string
}

func newSessionStore() *sessionStore {
	return &sessionStore{m: map[string]sessionData{}, logins: map[string]string{}}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// tokenCache remembers recently validated bearer tokens so every API call
// doesn't hit the userinfo endpoint.
type tokenCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]time.Time
}

func newTokenCache(ttl time.Duration) *tokenCache {
	return &tokenCache{ttl: ttl, m: map[string]time.Time{}}
}

func (t *tokenCache) valid(tok string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	until, ok := t.m[tok]
	return ok && time.Now().Before(until)
}

func (t *tokenCache) remember(tok string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[tok] = time.Now().Add(t.ttl)
}

// requireAuth guards a handler: a browser session cookie or a bearer
// token (validated against the default instance's userinfo endpoint)
// both pass. API callers get JSON 401; browsers get the login redirect.
func (s *Server) requireAuth(next http.Handler, isAPI bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("cp_session"); err == nil {
			s.sessions.mu.Lock()
			_, ok := s.sessions.m[c.Value]
			s.sessions.mu.Unlock()
			if ok {
				next.ServeHTTP(w, r)
				return
			}
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

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	state, verifier := randHex(16), oauth2.GenerateVerifier()
	s.sessions.mu.Lock()
	s.sessions.logins[state] = verifier
	s.sessions.mu.Unlock()
	http.Redirect(w, r, s.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	s.sessions.mu.Lock()
	verifier, ok := s.sessions.logins[state]
	delete(s.sessions.logins, state)
	s.sessions.mu.Unlock()
	if !ok {
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	tok, err := s.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		http.Error(w, "code exchange: "+err.Error(), http.StatusBadGateway)
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idToken, err := s.verifier.Verify(r.Context(), rawID)
	if err != nil {
		http.Error(w, "id token: "+err.Error(), http.StatusBadGateway)
		return
	}
	var claims struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "claims: "+err.Error(), http.StatusBadGateway)
		return
	}
	sid := randHex(32)
	s.sessions.mu.Lock()
	s.sessions.m[sid] = sessionData{Name: claims.Name, Email: claims.Email, IDToken: rawID}
	s.sessions.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "cp_session", Value: sid, Path: "/", HttpOnly: true})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("cp_session"); err == nil {
		s.sessions.mu.Lock()
		delete(s.sessions.m, c.Value)
		s.sessions.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "cp_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// user returns the session of an authenticated browser request, if any.
func (s *Server) user(r *http.Request) (sessionData, bool) {
	c, err := r.Cookie("cp_session")
	if err != nil {
		return sessionData{}, false
	}
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	d, ok := s.sessions.m[c.Value]
	return d, ok
}
