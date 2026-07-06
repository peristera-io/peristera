// Package oidcrp is the OIDC relying-party flow every Peristera browser
// app shares (auth-code + PKCE, session cookie, logout): the stub, the
// control plane, and Ergonomos. It was three near-identical copies before
// M3 (issue #2) — the rule-of-three extraction.
//
// It handles the *browser* flow only; bearer-token API auth (validating a
// token against the IdP) stays with the service that needs it, composed on
// top of Session.
package oidcrp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/peristera-io/peristera/lib/session"
)

// Config configures a RelyingParty.
type Config struct {
	Issuer      string // the tenant's (or default) Zitadel issuer
	ClientID    string // a public PKCE client in that issuer
	RedirectURL string // <public-url>/auth/callback
	// SuccessURL is where Callback sends the browser after login (default "/").
	SuccessURL string
	// PostLogoutURL is passed to the IdP end-session endpoint (default "/").
	PostLogoutURL string
	Scopes        []string      // default: openid, profile, email
	CookieName    string        // default: "session"
	Secure        bool          // set the Secure cookie flag (behind TLS)
	SessionTTL    time.Duration // default: 12h
}

// Claims is the subset of ID-token claims apps use.
type Claims struct {
	Subject string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
}

// Session is what a logged-in browser carries.
type Session struct {
	Claims  Claims
	IDToken string
	// AccessToken is the user's OIDC access token, retained so a service can
	// exchange it (RFC 8693, actor=service subject=user) when it must call
	// another Peristera service on the user's behalf (ADR-0017, the M5 S2S
	// model). It lives only in the server-side in-memory session store,
	// keyed by the session-cookie id — never sent to the browser. Empty if
	// the IdP returned no access token. AccessTokenExpiry bounds its use;
	// past it, the caller must re-authenticate (no refresh yet — a later
	// refinement).
	AccessToken       string
	AccessTokenExpiry time.Time
}

// RelyingParty runs the flow for one issuer+client.
type RelyingParty struct {
	cfg      Config
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	sessions *session.Store[Session]
	logins   *session.Store[string] // OIDC state → PKCE verifier
}

// New discovers the issuer and builds a RelyingParty.
func New(ctx context.Context, cfg Config) (*RelyingParty, error) {
	if cfg.SuccessURL == "" {
		cfg.SuccessURL = "/"
	}
	if cfg.PostLogoutURL == "" {
		cfg.PostLogoutURL = "/"
	}
	if cfg.CookieName == "" {
		cfg.CookieName = "session"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email"}
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidcrp: discovery: %w", err)
	}
	return &RelyingParty{
		cfg: cfg,
		oauth: oauth2.Config{
			ClientID:    cfg.ClientID,
			Endpoint:    provider.Endpoint(),
			RedirectURL: cfg.RedirectURL,
			Scopes:      cfg.Scopes,
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		sessions: session.NewStore[Session](cfg.SessionTTL),
		logins:   session.NewStore[string](10 * time.Minute),
	}, nil
}

func randID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// stateCookie names the short-lived cookie that binds the OIDC state to
// the browser that started the login (prevents login-CSRF: a state minted
// in one browser can't be redeemed in a victim's browser).
const stateCookie = "oidc_state"

// Login starts the auth-code + PKCE flow.
func (rp *RelyingParty) Login(w http.ResponseWriter, r *http.Request) {
	state, verifier := randID(16), oauth2.GenerateVerifier()
	rp.logins.Put(state, verifier)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/",
		HttpOnly: true, Secure: rp.cfg.Secure, SameSite: http.SameSiteLaxMode,
		MaxAge: 600, // the login must complete within 10 minutes
	})
	http.Redirect(w, r, rp.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

// Callback completes the flow, sets the session cookie, and redirects to
// SuccessURL.
func (rp *RelyingParty) Callback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	// The state must match the cookie set in Login — the browser that
	// started the flow must be the one completing it (login-CSRF defense).
	sc, err := r.Cookie(stateCookie)
	if err != nil || sc.Value == "" || sc.Value != state {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1})
	verifier, ok := rp.logins.Get(state)
	if !ok {
		http.Error(w, "unknown or expired login state", http.StatusBadRequest)
		return
	}
	rp.logins.Delete(state)

	tok, err := rp.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(verifier))
	if err != nil {
		http.Error(w, "code exchange: "+err.Error(), http.StatusBadGateway)
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	idToken, err := rp.verifier.Verify(r.Context(), rawID)
	if err != nil {
		http.Error(w, "id token: "+err.Error(), http.StatusBadGateway)
		return
	}
	var c Claims
	if err := idToken.Claims(&c); err != nil {
		http.Error(w, "claims: "+err.Error(), http.StatusBadGateway)
		return
	}
	sid := randID(32)
	rp.sessions.Put(sid, Session{
		Claims:            c,
		IDToken:           rawID,
		AccessToken:       tok.AccessToken,
		AccessTokenExpiry: tok.Expiry,
	})
	rp.setCookie(w, sid, rp.cfg.SessionTTL)
	http.Redirect(w, r, rp.cfg.SuccessURL, http.StatusFound)
}

// Logout clears the session and, if possible, ends the IdP session.
func (rp *RelyingParty) Logout(w http.ResponseWriter, r *http.Request) {
	var idToken string
	if c, err := r.Cookie(rp.cfg.CookieName); err == nil {
		if sess, ok := rp.sessions.Get(c.Value); ok {
			idToken = sess.IDToken
		}
		rp.sessions.Delete(c.Value)
	}
	rp.clearCookie(w)
	if idToken != "" {
		end := rp.cfg.Issuer + "/oidc/v1/end_session?" + url.Values{
			"id_token_hint":            {idToken},
			"post_logout_redirect_uri": {rp.cfg.PostLogoutURL},
		}.Encode()
		http.Redirect(w, r, end, http.StatusFound)
		return
	}
	http.Redirect(w, r, rp.cfg.PostLogoutURL, http.StatusFound)
}

// Session returns the current browser session, if any.
func (rp *RelyingParty) Session(r *http.Request) (Session, bool) {
	c, err := r.Cookie(rp.cfg.CookieName)
	if err != nil {
		return Session{}, false
	}
	return rp.sessions.Get(c.Value)
}

// Middleware guards next; when there is no valid session it calls
// onUnauthorized (which redirects a browser to Login, or writes a 401 for
// an API — the caller decides).
func (rp *RelyingParty) Middleware(next http.Handler, onUnauthorized http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := rp.Session(r); ok {
			next.ServeHTTP(w, r)
			return
		}
		onUnauthorized(w, r)
	})
}

// LoginPath is a convenience for the default onUnauthorized (browser).
func (rp *RelyingParty) RedirectToLogin(loginPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, loginPath, http.StatusFound)
	}
}

func (rp *RelyingParty) setCookie(w http.ResponseWriter, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: rp.cfg.CookieName, Value: value, Path: "/",
		HttpOnly: true, Secure: rp.cfg.Secure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(ttl.Seconds()),
	})
}

func (rp *RelyingParty) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: rp.cfg.CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: rp.cfg.Secure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}
