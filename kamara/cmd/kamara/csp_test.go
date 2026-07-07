package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContentSecurityPolicy(t *testing.T) {
	// Always present, restrictive defaults.
	base := contentSecurityPolicy("")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self' 'unsafe-eval'", // htmx hx-on; no unsafe-inline
		"object-src 'none'",
		"frame-ancestors 'none'",
		"frame-src 'none'",   // no office → no frame source
		"form-action 'self'", // no office target
	} {
		if !strings.Contains(base, want) {
			t.Errorf("CSP (no office) missing %q in %q", want, base)
		}
	}
	// script-src must not allow unsafe-inline (style-src legitimately does).
	for _, d := range strings.Split(base, "; ") {
		if strings.HasPrefix(d, "script-src") && strings.Contains(d, "unsafe-inline") {
			t.Errorf("script-src must not allow unsafe-inline: %q", d)
		}
	}

	// With office enabled, its origin is a frame + form target.
	office := "http://office.demo:9080"
	withOffice := contentSecurityPolicy(office)
	if !strings.Contains(withOffice, "frame-src "+office) {
		t.Errorf("CSP should allow office as a frame source: %q", withOffice)
	}
	if !strings.Contains(withOffice, "form-action 'self' "+office) {
		t.Errorf("CSP should allow office as a form target: %q", withOffice)
	}
}

func TestCSPMiddlewareSetsHeader(t *testing.T) {
	h := cspMiddleware("default-src 'self'", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("CSP header = %q", got)
	}
}
