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

	"github.com/peristera-io/peristera/kamara/internal/api"
	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
	"github.com/peristera-io/peristera/kamara/internal/file"
	"github.com/peristera-io/peristera/kamara/internal/store"
	"github.com/peristera-io/peristera/lib/authz"
	"github.com/peristera-io/peristera/lib/pii"
)

// fgaModel is Kamara's contribution to the tenant authorization model
// (ADR-0010): a file has an owner who is a user.
const fgaModel = `{
  "schema_version": "1.1",
  "type_definitions": [
    {"type": "user"},
    {"type": "kamara/file",
     "relations": {"owner": {"this": {}}},
     "metadata": {"relations": {"owner": {"directly_related_user_types": [{"type": "user"}]}}}}
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.Handle("/v1/", h.Routes())

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

func issuerHost(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return issuer
	}
	return u.Hostname()
}
