// Package server is the control plane's product surface: the HTMX UI and
// the /api/v1 HTTP API (spec: api/openapi.yaml — the spec is authored
// first, handlers implement the generated interface). It runs inside the
// controller process as a manager Runnable (ADR-0008: one binary).
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/internal/server/gen"
	"github.com/peristera-io/peristera/control-plane/internal/zitadel"
)

type Config struct {
	ListenAddr string // e.g. :8090
	PublicURL  string // e.g. http://localhost:8090 or http://cp.<base>:9080
	// Issuer of the DEFAULT Zitadel instance — operators live there.
	Issuer string
}

type Server struct {
	K8s client.Client
	IAM *zitadel.Client
	Cfg Config

	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	sessions *sessionStore
	tokens   *tokenCache
}

// Start implements manager.Runnable: bootstrap our own OIDC app in the
// default instance (idempotent — the same path tenants take), then serve.
func (s *Server) Start(ctx context.Context) error {
	lg := log.FromContext(ctx).WithName("server")

	orgID, err := s.IAM.FirstOrgID(ctx, s.Cfg.Issuer)
	if err != nil {
		return fmt.Errorf("default org: %w", err)
	}
	clientID, err := s.IAM.EnsureWebApp(ctx, s.Cfg.Issuer, orgID, "control-plane",
		[]string{s.Cfg.PublicURL + "/auth/callback"}, []string{s.Cfg.PublicURL + "/"})
	if err != nil {
		return fmt.Errorf("ensuring own OIDC app: %w", err)
	}

	provider, err := oidc.NewProvider(ctx, s.Cfg.Issuer)
	if err != nil {
		return fmt.Errorf("oidc discovery: %w", err)
	}
	s.verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	s.oauth = oauth2.Config{
		ClientID:    clientID,
		Endpoint:    provider.Endpoint(),
		RedirectURL: s.Cfg.PublicURL + "/auth/callback",
		Scopes:      []string{oidc.ScopeOpenID, "profile", "email"},
	}
	s.sessions = newSessionStore()
	s.tokens = newTokenCache(time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /auth/login", s.authLogin)
	mux.HandleFunc("GET /auth/callback", s.authCallback)
	mux.HandleFunc("GET /auth/logout", s.authLogout)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1",
		s.requireAuth(gen.Handler(&api{s}), true)))
	mux.Handle("/", s.requireAuth(s.uiMux(), false))

	srv := &http.Server{Addr: s.Cfg.ListenAddr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	lg.Info("control-plane UI/API listening", "addr", s.Cfg.ListenAddr, "publicURL", s.Cfg.PublicURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
