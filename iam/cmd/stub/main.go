// The stub relying party: the login shape every Peristera app copies, now
// built on lib/oidcrp (the shared flow — issue #2). Also the first entry
// of the control plane's tenant app catalog.
//
// Catalog env contract (ADR-0013):
//
//	OIDC_ISSUER    the tenant's issuer (its Zitadel virtual instance)
//	OIDC_CLIENT_ID client ID of the tenant's public (PKCE) app
//	PUBLIC_URL     this app's external base URL (derives redirect URLs)
//	LISTEN_ADDR    listen address (default :5556)
package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/peristera-io/peristera/lib/oidcrp"
)

var page = template.Must(template.New("").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Peristera IAM stub</title></head>
<body>
<h1>Peristera IAM — stub</h1>
<p>Issuer: <code>{{.Issuer}}</code></p>
{{if .LoggedIn}}
<p>Logged in as <strong>{{.Name}}</strong> ({{.Email}})</p>
<p><a href="/auth/logout">Log out</a></p>
{{else}}
<p><a href="/auth/login">Log in</a></p>
{{end}}
</body></html>
`))

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func publicURL() string {
	return strings.TrimSuffix(env("PUBLIC_URL", "http://localhost:5556"), "/")
}

func main() {
	issuer := env("OIDC_ISSUER", "http://demo.127.0.0.1.sslip.io:9080")
	clientID := os.Getenv("OIDC_CLIENT_ID")
	if clientID == "" {
		log.Fatal("OIDC_CLIENT_ID is required")
	}

	rp, err := oidcrp.New(context.Background(), oidcrp.Config{
		Issuer:      issuer,
		ClientID:    clientID,
		RedirectURL: publicURL() + "/auth/callback",
		CookieName:  "stub_session",
	})
	if err != nil {
		log.Fatalf("oidcrp: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		sess, ok := rp.Session(r)
		_ = page.Execute(w, map[string]any{
			"Issuer": issuer, "LoggedIn": ok,
			"Name": sess.Claims.Name, "Email": sess.Claims.Email,
		})
	})
	mux.HandleFunc("GET /auth/login", rp.Login)
	mux.HandleFunc("GET /auth/callback", rp.Callback)
	mux.HandleFunc("GET /auth/logout", rp.Logout)

	addr := env("LISTEN_ADDR", ":5556")
	log.Printf("stub relying party on %s (issuer %s)", addr, issuer)
	log.Fatal(http.ListenAndServe(addr, mux))
}
