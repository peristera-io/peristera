package oidcrp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginGuard(t *testing.T) {
	const self = "https://app.tenant.example"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	guard := SameOriginGuard(self, next)

	cases := []struct {
		name       string
		method     string
		secFetch   string
		origin     string
		wantStatus int
	}{
		{"GET is always allowed", http.MethodGet, "cross-site", "https://evil.example", http.StatusOK},
		{"HEAD is always allowed", http.MethodHead, "cross-site", "", http.StatusOK},
		{"POST same-origin", http.MethodPost, "same-origin", "", http.StatusOK},
		{"POST user-initiated (none)", http.MethodPost, "none", "", http.StatusOK},
		{"POST cross-site rejected", http.MethodPost, "cross-site", "", http.StatusForbidden},
		{"POST same-site (sibling subdomain) rejected", http.MethodPost, "same-site", "", http.StatusForbidden},
		{"DELETE cross-site rejected", http.MethodDelete, "cross-site", "", http.StatusForbidden},
		{"POST no sec-fetch, matching Origin", http.MethodPost, "", self, http.StatusOK},
		{"POST no sec-fetch, foreign Origin rejected", http.MethodPost, "", "https://evil.example", http.StatusForbidden},
		{"POST no sec-fetch, no Origin (non-browser)", http.MethodPost, "", "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, self+"/mutate", nil)
			if tc.secFetch != "" {
				r.Header.Set("Sec-Fetch-Site", tc.secFetch)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			guard.ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("got %d, want %d", w.Code, tc.wantStatus)
			}
		})
	}
}
