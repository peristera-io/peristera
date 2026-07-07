package api

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func jwt(exp int64) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	body, _ := json.Marshal(map[string]any{"sub": "u1", "exp": exp})
	return hdr + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func TestTokenExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"expired JWT", jwt(now.Unix() - 60), true},
		{"valid JWT", jwt(now.Unix() + 3600), false},
		{"opaque token (not a JWT)", "abc123opaque", false},
		{"jwe-like 5 segments", "a.b.c.d.e", false},
		{"JWT without exp", func() string {
			h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
			b := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u1"}`))
			return h + "." + b + ".sig"
		}(), false},
		{"garbage payload", "a." + base64.RawURLEncoding.EncodeToString([]byte("notjson")) + ".c", false},
	}
	for _, c := range cases {
		if got := tokenExpired(c.token, now); got != c.want {
			t.Errorf("%s: tokenExpired = %v, want %v", c.name, got, c.want)
		}
	}
}
