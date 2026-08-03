package zitadel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testClient builds a Client wired to a local test server, with a throwaway
// signing key (the fake server never verifies the bearer JWT).
func testClient(t *testing.T, srv *httptest.Server, devMode bool) *Client {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{
		BaseURL: srv.URL, UserID: "test-user", DevMode: devMode,
		key: key, http: &http.Client{Timeout: 5 * time.Second},
	}
}

// The create branch of EnsureWebApp must provision the OIDC app with the
// client's DevMode, not a hardcoded true — on the cloud (https issuer,
// DevMode=false) devMode relaxes Zitadel's redirect-URI/HTTPS validation,
// which #5 gated off and #65 found regressed on the create path.
func TestEnsureWebAppCreateDevMode(t *testing.T) {
	for _, devMode := range []bool{false, true} {
		var createBody map[string]any
		mux := http.NewServeMux()
		mux.HandleFunc("/management/v1/projects/_search", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{
				"result": []map[string]any{{"id": "proj1"}},
			})
		})
		mux.HandleFunc("/management/v1/projects/proj1/apps/_search", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"result": []any{}}) // no app yet -> create branch
		})
		mux.HandleFunc("/management/v1/projects/proj1/apps/oidc", func(w http.ResponseWriter, r *http.Request) {
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Errorf("decoding create body: %v", err)
			}
			json.NewEncoder(w).Encode(map[string]any{"clientId": "client1"})
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		})
		srv := httptest.NewServer(mux)

		c := testClient(t, srv, devMode)
		id, err := c.EnsureWebApp(context.Background(), srv.URL, "org1", "kamara",
			[]string{"https://kamara.demo.example/auth/callback"}, []string{"https://kamara.demo.example/"})
		srv.Close()
		if err != nil {
			t.Fatalf("EnsureWebApp(devMode=%v): %v", devMode, err)
		}
		if id != "client1" {
			t.Errorf("clientID = %q, want client1", id)
		}
		got, ok := createBody["devMode"].(bool)
		if !ok || got != devMode {
			t.Errorf("create devMode = %v (present=%v), want %v", got, ok, devMode)
		}
	}
}

// An existing app whose devMode drifted from the client's (e.g. created while
// #65 hardcoded devMode:true) must be healed by the reconcile PUT even when
// the redirect-URI set is already complete — otherwise pre-fix production
// apps would keep Zitadel's relaxed redirect validation forever.
func TestEnsureWebAppHealsDevModeDrift(t *testing.T) {
	var putBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/management/v1/projects/_search", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{"id": "proj1"}}})
	})
	mux.HandleFunc("/management/v1/projects/proj1/apps/_search", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": []map[string]any{{
			"id": "app1",
			"oidcConfig": map[string]any{
				"clientId":               "client1",
				"redirectUris":           []string{"https://kamara.demo.example/auth/callback"},
				"postLogoutRedirectUris": []string{"https://kamara.demo.example/"},
				"devMode":                true, // drifted: created pre-fix
			},
		}}})
	})
	mux.HandleFunc("/management/v1/projects/proj1/apps/app1/oidc_config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Errorf("decoding put body: %v", err)
		}
		w.Write([]byte("{}"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := testClient(t, srv, false) // production client: devMode must be off
	id, err := c.EnsureWebApp(context.Background(), srv.URL, "org1", "kamara",
		[]string{"https://kamara.demo.example/auth/callback"}, []string{"https://kamara.demo.example/"})
	if err != nil {
		t.Fatalf("EnsureWebApp: %v", err)
	}
	if id != "client1" {
		t.Errorf("clientID = %q, want client1", id)
	}
	if putBody == nil {
		t.Fatal("devMode drift must trigger the reconcile PUT even with complete redirect URIs")
	}
	if got, ok := putBody["devMode"].(bool); !ok || got {
		t.Errorf("healed devMode = %v (present=%v), want false", got, ok)
	}
}

// NewFromKeyFile derives DevMode from the issuer scheme: https (production)
// must come up with devMode off.
func TestDevModeFollowsScheme(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	for base, want := range map[string]bool{
		"https://iam.peristera.app":          false,
		"http://iam.127.0.0.1.sslip.io:9080": true,
	} {
		c, err := NewFromKeyFile(base, "u", path)
		if err != nil {
			t.Fatalf("NewFromKeyFile(%q): %v", base, err)
		}
		if c.DevMode != want {
			t.Errorf("DevMode for %q = %v, want %v", base, c.DevMode, want)
		}
	}
}
