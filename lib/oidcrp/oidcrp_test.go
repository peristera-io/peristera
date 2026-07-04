package oidcrp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/peristera-io/peristera/lib/session"
)

// newTestRP builds a RelyingParty without OIDC discovery, for testing the
// session/cookie/middleware surface (the flow that doesn't touch the IdP).
func newTestRP() *RelyingParty {
	return &RelyingParty{
		cfg:      Config{CookieName: "session", PostLogoutURL: "/", Issuer: "http://issuer.example"},
		sessions: session.NewStore[Session](time.Hour),
		logins:   session.NewStore[string](time.Minute),
	}
}

func TestSessionRoundTripAndCookie(t *testing.T) {
	rp := newTestRP()
	rp.sessions.Put("sid1", Session{Claims: Claims{Subject: "u", Name: "U"}})

	r := httptest.NewRequest("GET", "/", nil)
	if _, ok := rp.Session(r); ok {
		t.Error("no cookie should mean no session")
	}
	r.AddCookie(&http.Cookie{Name: "session", Value: "sid1"})
	sess, ok := rp.Session(r)
	if !ok || sess.Claims.Name != "U" {
		t.Errorf("Session = %+v,%v", sess, ok)
	}
}

func TestMiddlewareGuards(t *testing.T) {
	rp := newTestRP()
	rp.sessions.Put("good", Session{Claims: Claims{Subject: "u"}})

	nextCalled := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
	unauth := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(401) })
	h := rp.Middleware(next, unauth)

	// No cookie → onUnauthorized.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if nextCalled || rec.Code != 401 {
		t.Errorf("unauth path wrong: next=%v code=%d", nextCalled, rec.Code)
	}
	// Valid cookie → next.
	rec = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "good"})
	h.ServeHTTP(rec, r)
	if !nextCalled {
		t.Error("valid session did not reach next")
	}
}

func TestLogoutClearsCookieAndSession(t *testing.T) {
	rp := newTestRP()
	rp.sessions.Put("sid", Session{Claims: Claims{Subject: "u"}}) // no IDToken → plain redirect

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/auth/logout", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: "sid"})
	rp.Logout(rec, r)

	if _, ok := rp.sessions.Get("sid"); ok {
		t.Error("session not deleted on logout")
	}
	if rec.Code != http.StatusFound {
		t.Errorf("logout code = %d", rec.Code)
	}
	// Cookie cleared (MaxAge<0).
	if c := rec.Result().Cookies(); len(c) == 0 || c[0].MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %+v", c)
	}
}
