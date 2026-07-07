package server

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func mkJWT(aud any) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	payload := map[string]any{"sub": "u1"}
	if aud != nil {
		payload["aud"] = aud
	}
	body, _ := json.Marshal(payload)
	return hdr + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func TestAudienceOK(t *testing.T) {
	const cp = "control-plane-client"
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"opaque token (PAT) falls through", "pat_abc123opaque", true},
		{"JWT aud array includes us", mkJWT([]string{"other", cp}), true},
		{"JWT aud string is us", mkJWT(cp), true},
		{"JWT aud array excludes us", mkJWT([]string{"tenant-app", "proj"}), false},
		{"JWT aud string is another app", mkJWT("tenant-app"), false},
		{"JWT with no aud falls through", mkJWT(nil), true},
		{"unparseable payload falls through", "a.@@@.c", true},
	}
	for _, c := range cases {
		if got := audienceOK(c.token, cp); got != c.want {
			t.Errorf("%s: audienceOK = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestParseAud(t *testing.T) {
	if got := parseAud(json.RawMessage(`"one"`)); len(got) != 1 || got[0] != "one" {
		t.Errorf("string aud: %v", got)
	}
	if got := parseAud(json.RawMessage(`["a","b"]`)); len(got) != 2 {
		t.Errorf("array aud: %v", got)
	}
	if got := parseAud(nil); got != nil {
		t.Errorf("empty aud: %v", got)
	}
}
