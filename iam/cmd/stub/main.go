// The M1 spike's stub relying party: proves the OIDC login shape every
// Peristera app will copy — auth-code + PKCE against a tenant's own
// Zitadel virtual instance (its issuer is the tenant domain).
//
// Configuration follows the catalog env contract every Peristera app pod
// receives from the control plane (ADR-0008):
//
//	OIDC_ISSUER    the tenant's issuer (its Zitadel virtual instance)
//	OIDC_CLIENT_ID client ID of the tenant's public (PKCE) app
//	PUBLIC_URL     this app's external base URL (derives redirect URLs)
//	LISTEN_ADDR    listen address (default :5556)
//
// (Legacy STUB_* variables remain as fallbacks for bare local runs.)
// Sessions are an in-memory map — this is spike code; the real session
// convention (and its ADR) comes with the control plane in M2.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type claims struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Sub   string `json:"sub"`
}

type session struct {
	Claims  claims
	IDToken string
}

type server struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	issuer   string

	mu       sync.Mutex
	sessions map[string]session // sid → session
	logins   map[string]string  // state → PKCE verifier
}

var page = template.Must(template.New("").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Peristera IAM stub</title></head>
<body>
<h1>Peristera IAM — M1 stub</h1>
<p>Issuer: <code>{{.Issuer}}</code></p>
{{if .LoggedIn}}
<p>Logged in as <strong>{{.Claims.Name}}</strong> ({{.Claims.Email}})</p>
<p><a href="/auth/logout">Log out</a></p>
{{else}}
<p><a href="/auth/login">Log in</a></p>
{{end}}
</body></html>
`))

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (s *server) session(r *http.Request) (session, bool) {
	c, err := r.Cookie("stub_session")
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[c.Value]
	return sess, ok
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r)
	_ = page.Execute(w, map[string]any{
		"Issuer": s.issuer, "LoggedIn": ok, "Claims": sess.Claims,
	})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	state, verifier := randHex(16), oauth2.GenerateVerifier()
	s.mu.Lock()
	s.logins[state] = verifier
	s.mu.Unlock()
	http.Redirect(w, r, s.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (s *server) callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	s.mu.Lock()
	verifier, ok := s.logins[state]
	delete(s.logins, state)
	s.mu.Unlock()
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
	var cl claims
	if err := idToken.Claims(&cl); err != nil {
		http.Error(w, "claims: "+err.Error(), http.StatusBadGateway)
		return
	}
	sid := randHex(32)
	s.mu.Lock()
	s.sessions[sid] = session{Claims: cl, IDToken: rawID}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "stub_session", Value: sid, Path: "/", HttpOnly: true})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r)
	if c, err := r.Cookie("stub_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "stub_session", Value: "", Path: "/", MaxAge: -1})
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	end := s.issuer + "/oidc/v1/end_session?" + url.Values{
		"id_token_hint":            {sess.IDToken},
		"post_logout_redirect_uri": {publicURL() + "/"},
	}.Encode()
	http.Redirect(w, r, end, http.StatusFound)
}

func publicURL() string {
	return strings.TrimSuffix(env("PUBLIC_URL", "http://localhost:5556"), "/")
}

func main() {
	issuer := env("OIDC_ISSUER", env("STUB_ISSUER", "http://demo.127.0.0.1.sslip.io:9080"))
	clientID := env("OIDC_CLIENT_ID", os.Getenv("STUB_CLIENT_ID"))
	if clientID == "" {
		log.Fatal("OIDC_CLIENT_ID is required")
	}

	provider, err := oidc.NewProvider(context.Background(), issuer)
	if err != nil {
		log.Fatalf("oidc discovery: %v", err)
	}
	s := &server{
		issuer:   issuer,
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		oauth: oauth2.Config{
			ClientID:    clientID,
			Endpoint:    provider.Endpoint(),
			RedirectURL: publicURL() + "/auth/callback",
			Scopes:      []string{oidc.ScopeOpenID, "profile", "email"},
		},
		sessions: map[string]session{},
		logins:   map[string]string{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /auth/login", s.login)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("GET /auth/logout", s.logout)

	addr := env("LISTEN_ADDR", env("STUB_ADDR", ":5556"))
	log.Printf("stub relying party on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}
