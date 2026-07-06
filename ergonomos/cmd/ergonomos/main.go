// Ergonomos — the single-user task stub (M3b). Boots by running its goose
// migrations, connecting to its per-app database and the tenant's OpenFGA,
// then serves the task UI behind OIDC login. Configuration is the catalog
// env contract (ADR-0013).
package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/peristera-io/peristera/ergonomos/internal/kamara"
	"github.com/peristera-io/peristera/ergonomos/internal/store"
	"github.com/peristera-io/peristera/ergonomos/internal/task"
	"github.com/peristera-io/peristera/lib/authz"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
	"github.com/peristera-io/peristera/lib/svcauth"
)

// fgaModel is Ergonomos's contribution to the tenant authorization model
// (ADR-0010): a task has an owner who is a user.
const fgaModel = `{
  "schema_version": "1.1",
  "type_definitions": [
    {"type": "user"},
    {"type": "ergonomos/task",
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

	az, err := authz.Connect(ctx, mustEnv("OPENFGA_API_URL"), "ergonomos", []byte(fgaModel),
		authz.WithToken(os.Getenv("OPENFGA_API_TOKEN")))
	if err != nil {
		log.Fatalf("openfga: %v", err)
	}

	issuer := mustEnv("OIDC_ISSUER")
	publicURL := strings.TrimSuffix(env("PUBLIC_URL", "http://localhost:5570"), "/")
	// As an S2S caller (ADR-0017), request the project-audience scope so the
	// user's login token is exchangeable for a call to Kamara on their
	// behalf. OIDC_PROJECT_ID is injected only for apps that declare Calls.
	scopes := []string{"openid", "profile", "email"}
	projectID := os.Getenv("OIDC_PROJECT_ID")
	if projectID != "" {
		scopes = append(scopes, svcauth.ProjectAudienceScope(projectID))
	}
	rp, err := oidcrp.New(ctx, oidcrp.Config{
		Issuer:      issuer,
		ClientID:    mustEnv("OIDC_CLIENT_ID"),
		RedirectURL: publicURL + "/auth/callback",
		CookieName:  "ergonomos_session",
		Scopes:      scopes,
	})
	if err != nil {
		log.Fatalf("oidc: %v", err)
	}

	// The on-behalf-of Kamara client (ADR-0017), wired only when provisioned
	// as an S2S caller (the S2S key + project id + callee URL are present).
	var kam *kamara.Client
	if keyPath := os.Getenv("SVCAUTH_KEY_FILE"); keyPath != "" && projectID != "" {
		keyJSON, err := os.ReadFile(keyPath)
		if err != nil {
			log.Fatalf("svcauth key: %v", err)
		}
		ex, err := svcauth.NewExchanger(issuer, projectID, keyJSON)
		if err != nil {
			log.Fatalf("svcauth: %v", err)
		}
		kam = kamara.New(mustEnv("KAMARA_URL"), ex)
	}

	// db satisfies task.TxRunner: mutations run row+audit+search in one
	// transaction (ADR-0015); the OpenFGA client is the out-of-transaction
	// authorization store.
	svc := task.NewService(pii.Default, db, az)

	// The data subject is the logged-in user: instance = the issuer's host
	// (the tenant's permanent domain, ADR-0009 §2), user = the OIDC sub.
	instance := issuerHost(issuer)
	app := &webApp{svc: svc, rp: rp, instance: instance, issuer: issuer, kamara: kam}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /auth/login", rp.Login)
	mux.HandleFunc("GET /auth/callback", rp.Callback)
	mux.HandleFunc("GET /auth/logout", rp.Logout)
	mux.Handle("/", rp.Middleware(app.routes(), rp.RedirectToLogin("/auth/login")))

	addr := env("LISTEN_ADDR", ":5570")
	log.Printf("ergonomos on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func issuerHost(issuer string) string {
	u, err := url.Parse(issuer)
	if err != nil {
		return issuer
	}
	return u.Hostname()
}
