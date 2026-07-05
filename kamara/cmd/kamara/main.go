// Kamara — the per-tenant file store (M4a). Boots by running its goose
// migrations, connecting to its per-app database, the tenant's OpenFGA, its
// blob backend (a per-tenant filesystem volume), and the per-tenant
// data-encryption key, then serves the storage API. Callers authenticate
// with a bearer token validated against the tenant OIDC provider; the
// caller's subject owns the files. Configuration is the catalog env
// contract (ADR-0013).
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/peristera-io/peristera/kamara/internal/api"
	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/store"
	"github.com/peristera-io/peristera/kamara/internal/web"
	"github.com/peristera-io/peristera/lib/authz"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

// fgaModel is Kamara's contribution to the tenant authorization model
// (ADR-0010; model accretion #19). A folder and a file each have an owner
// (a user) and an optional parent folder; access is `can_access` — owner,
// or inherited up the folder chain (`can_access from parent`). Per-owner
// trees today; folder sharing later adds tuples, not a new model shape
// (Kamara ADR-0002).
const fgaModel = `{
  "schema_version": "1.1",
  "type_definitions": [
    {"type": "user"},
    {"type": "kamara/folder",
     "relations": {
       "owner": {"this": {}},
       "parent": {"this": {}},
       "can_access": {"union": {"child": [
         {"computedUserset": {"relation": "owner"}},
         {"tupleToUserset": {"tupleset": {"relation": "parent"}, "computedUserset": {"relation": "can_access"}}}
       ]}}
     },
     "metadata": {"relations": {
       "owner": {"directly_related_user_types": [{"type": "user"}]},
       "parent": {"directly_related_user_types": [{"type": "kamara/folder"}]}
     }}},
    {"type": "kamara/file",
     "relations": {
       "owner": {"this": {}},
       "parent": {"this": {}},
       "can_access": {"union": {"child": [
         {"computedUserset": {"relation": "owner"}},
         {"tupleToUserset": {"tupleset": {"relation": "parent"}, "computedUserset": {"relation": "can_access"}}}
       ]}}
     },
     "metadata": {"relations": {
       "owner": {"directly_related_user_types": [{"type": "user"}]},
       "parent": {"directly_related_user_types": [{"type": "kamara/folder"}]}
     }}}
  ]
}`

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("%s is required", key)
	}
	return v
}

func main() {
	ctx := context.Background()

	db, err := store.Open(mustEnv("DATABASE_DSN"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	az, err := authz.Connect(ctx, mustEnv("OPENFGA_API_URL"), "kamara", []byte(fgaModel))
	if err != nil {
		log.Fatalf("openfga: %v", err)
	}

	blobs, err := blob.NewFS(mustEnv("KAMARA_BLOB_DIR"))
	if err != nil {
		log.Fatalf("blob store: %v", err)
	}

	// The subject/tenant instance is the issuer's host — the tenant's
	// permanent domain (ADR-0009 §2). It is also the crypto tenant, so a
	// chunk's associated data binds to this tenant (Kamara ADR-0001 §6).
	issuer := mustEnv("OIDC_ISSUER")
	instance := issuerHost(issuer)

	cipher, err := crypto.New(loadDEK(), instance)
	if err != nil {
		log.Fatalf("dek: %v", err)
	}

	// db satisfies file.TxRunner: object+version+manifest+audit+search run
	// in one transaction (ADR-0015); OpenFGA is the out-of-transaction
	// authorization store; blobs are written durably before the commit.
	svc := file.NewService(pii.Default, db, az, blobs, cipher)

	auth := api.NewUserinfoAuth(issuer, instance, 0)
	h := api.New(svc, auth, 0)

	// Browser UI: the OIDC relying-party (cookie session) beside the bearer
	// API — same service, two front doors. Both resolve to a pii.Subject.
	publicURL := strings.TrimSuffix(env("PUBLIC_URL", "http://localhost:5580"), "/")
	rp, err := oidcrp.New(ctx, oidcrp.Config{
		Issuer:      issuer,
		ClientID:    mustEnv("OIDC_CLIENT_ID"),
		RedirectURL: publicURL + "/auth/callback",
		CookieName:  "kamara_session",
	})
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}
	app := &webApp{svc: svc, rp: rp, instance: instance}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /style.css", serveCSS)
	mux.Handle("/v1/", h.Routes()) // bearer API (authed inside)
	mux.HandleFunc("GET /auth/login", rp.Login)
	mux.HandleFunc("GET /auth/callback", rp.Callback)
	mux.HandleFunc("GET /auth/logout", rp.Logout)
	mux.Handle("/", rp.Middleware(app.routes(), rp.RedirectToLogin("/auth/login")))

	addr := env("LISTEN_ADDR", ":5580")
	log.Printf("kamara on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// loadDEK reads the per-tenant data-encryption key: a mounted secret file
// (KAMARA_DEK_FILE, the production path — a k8s Secret volume) takes
// precedence over a base64 env (KAMARA_DEK, convenient in dev). The key
// must be crypto.KeySize bytes.
func loadDEK() []byte {
	if path := os.Getenv("KAMARA_DEK_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("dek file: %v", err)
		}
		return decodeKey(b)
	}
	if v := os.Getenv("KAMARA_DEK"); v != "" {
		return decodeKey([]byte(v))
	}
	log.Fatal("KAMARA_DEK_FILE or KAMARA_DEK is required")
	return nil
}

// decodeKey accepts either raw key bytes (exactly KeySize) or base64. A
// mounted k8s Secret is base64 text with a trailing newline, so surrounding
// whitespace is trimmed before the length check and decode.
func decodeKey(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if len(b) == crypto.KeySize {
		return b
	}
	dec, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		log.Fatalf("dek: not %d raw bytes and not valid base64: %v", crypto.KeySize, err)
	}
	return dec
}

// serveCSS serves the embedded Tailwind stylesheet (unauthenticated — the
// login page needs it too), with a content type and long cache.
func serveCSS(w http.ResponseWriter, _ *http.Request) {
	css, err := web.Stylesheet()
	if err != nil {
		http.Error(w, "stylesheet unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(css)
}

func issuerHost(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return issuer
	}
	return u.Hostname()
}
