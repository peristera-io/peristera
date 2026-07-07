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
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/internal/server/gen"
	"github.com/peristera-io/peristera/control-plane/internal/zitadel"
	"github.com/peristera-io/peristera/lib/authz"
	"github.com/peristera-io/peristera/lib/oidcrp"
	"github.com/peristera-io/peristera/lib/pii"
)

type Config struct {
	ListenAddr string // e.g. :8090
	PublicURL  string // e.g. http://localhost:8090 or http://cp.<base>:9080
	// Issuer of the DEFAULT Zitadel instance — operators live there.
	Issuer string
	// Operator authorization (ADR-0019): the platform OpenFGA the control
	// plane checks the `operator` relation against, its preshared key, and the
	// bootstrap operator subjects seeded on startup.
	OpenFGAURL   string
	OpenFGAToken string
	OperatorSubs []string
}

type Server struct {
	K8s client.Client
	IAM *zitadel.Client
	Cfg Config

	rp       *oidcrp.RelyingParty // browser flow (shared, lib/oidcrp)
	tokens   *tokenCache          // bearer-token validation cache (API path)
	authz    *authz.Client        // platform operator authorization (ADR-0019)
	clientID string               // our own OIDC client id (bearer audience)
}

// NeedLeaderElection: the UI/API serves on every replica; only the
// reconciler is single-active. Without this, a rolling update deadlocks —
// the new pod's server would wait for the lease the old pod still holds,
// and the readiness probe would never pass.
func (s *Server) NeedLeaderElection() bool { return false }

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
	s.clientID = clientID

	// Operator authorization (ADR-0019): connect the platform OpenFGA, install
	// the operator model, and seed the bootstrap operators so the first
	// operator is never locked out. The store is in-memory and re-seeded here
	// on every startup.
	s.authz, err = authz.Connect(ctx, s.Cfg.OpenFGAURL, "control-plane", operatorModel,
		authz.WithToken(s.Cfg.OpenFGAToken))
	if err != nil {
		return fmt.Errorf("operator authz: %w", err)
	}
	inst := issuerHost(s.Cfg.Issuer)
	for _, sub := range s.Cfg.OperatorSubs {
		if err := s.authz.Write(ctx, pii.Subject{Instance: inst, UserID: sub}, operatorRelation, platformObject); err != nil {
			return fmt.Errorf("seeding operator %s: %w", sub, err)
		}
	}
	if len(s.Cfg.OperatorSubs) == 0 {
		lg.Info("WARNING: no OPERATOR_SUBJECTS seeded — no one can operate the control plane (ADR-0019)")
	} else {
		lg.Info("seeded control-plane operators", "count", len(s.Cfg.OperatorSubs))
	}

	s.rp, err = oidcrp.New(ctx, oidcrp.Config{
		Issuer:      s.Cfg.Issuer,
		ClientID:    clientID,
		RedirectURL: s.Cfg.PublicURL + "/auth/callback",
		CookieName:  "cp_session",
		Secure:      strings.HasPrefix(s.Cfg.PublicURL, "https://"),
	})
	if err != nil {
		return fmt.Errorf("oidc relying party: %w", err)
	}
	s.tokens = newTokenCache(time.Minute)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /auth/login", s.rp.Login)
	mux.HandleFunc("GET /auth/callback", s.rp.Callback)
	mux.HandleFunc("GET /auth/logout", s.rp.Logout)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1",
		s.requireAuth(gen.Handler(&api{s}), true)))
	// CSRF guard (#4) on the cookie-authed HTMX UI. (The bearer /api/v1 also
	// accepts the session cookie today — tightening that is part of #1.)
	mux.Handle("/", s.requireAuth(oidcrp.SameOriginGuard(s.Cfg.PublicURL, s.uiMux()), false))

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
