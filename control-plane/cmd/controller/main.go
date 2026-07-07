// The Peristera control-plane controller: reconciles Tenant resources
// (ADR-0008). Runs out-of-cluster against the current kubeconfig during
// development; in-cluster deployment arrives with the M2 UI sessions.
package main

import (
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
	"github.com/peristera-io/peristera/control-plane/internal/controller"
	"github.com/peristera-io/peristera/control-plane/internal/server"
	"github.com/peristera-io/peristera/control-plane/internal/zitadel"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// splitList parses a comma-separated env value into a trimmed, non-empty list.
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	lg := ctrl.Log.WithName("setup")

	scheme := clientgoscheme.Scheme
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		lg.Error(err, "adding scheme")
		os.Exit(1)
	}

	// Leader election: only one controller acts, even when a local dev
	// run overlaps the in-cluster deployment.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          true,
		LeaderElectionID:        "control-plane.peristera.io",
		LeaderElectionNamespace: env("CP_NAMESPACE", "peristera-system"),
		// Don't cache Secrets: the informer cache would need cluster-wide
		// secrets list/watch (the whole point of #7). Reads go direct via the
		// API (get by name), so the SA needs only get + create, never list all
		// secrets. The reconciler only ever Gets secrets by name, never Lists.
		Client: client.Options{Cache: &client.CacheOptions{
			DisableFor: []client.Object{&corev1.Secret{}},
		}},
	})
	if err != nil {
		lg.Error(err, "creating manager")
		os.Exit(1)
	}
	rec := &controller.TenantReconciler{
		Client:       mgr.GetClient(),
		BaseDomain:   env("TENANT_BASE_DOMAIN", "127.0.0.1.sslip.io"),
		ExternalPort: env("TENANT_EXTERNAL_PORT", "9080"),
		LoginDomain:  env("ZITADEL_EXTERNAL_DOMAIN", "iam.127.0.0.1.sslip.io"),
		ImagePrefix:  env("IMAGE_PREFIX", "peristera-"),
		ImageTag:     env("IMAGE_TAG", "dev"),
		URLScheme:    env("TENANT_SCHEME", "http"),
		TLSIssuer:    env("TENANT_TLS_ISSUER", ""),
	}
	// IAM provisioning switches on when the system-user key is provided
	// (dev: a file path; in-cluster: the mounted admin-client-tls Secret).
	if keyPath := os.Getenv("SYSTEM_USER_KEY"); keyPath != "" {
		iam, err := zitadel.NewFromKeyFile(
			env("ZITADEL_BASE_URL", "http://iam.127.0.0.1.sslip.io:9080"),
			env("SYSTEM_USER_ID", "admin-client"),
			keyPath,
		)
		if err != nil {
			lg.Error(err, "loading system user key")
			os.Exit(1)
		}
		rec.IAM = iam
	} else {
		lg.Info("SYSTEM_USER_KEY not set — IAM provisioning disabled")
	}
	if err := rec.SetupWithManager(mgr); err != nil {
		lg.Error(err, "setting up tenant reconciler")
		os.Exit(1)
	}
	// The UI/API (one binary, ADR-0008) needs the IAM client for its own
	// OIDC bootstrap and bearer validation.
	if rec.IAM != nil {
		if err := mgr.Add(&server.Server{
			K8s: mgr.GetClient(),
			IAM: rec.IAM,
			Cfg: server.Config{
				ListenAddr:   env("CP_LISTEN_ADDR", ":8090"),
				PublicURL:    env("CP_PUBLIC_URL", "http://localhost:8090"),
				Issuer:       env("ZITADEL_BASE_URL", "http://iam.127.0.0.1.sslip.io:9080"),
				OpenFGAURL:   env("OPENFGA_API_URL", "http://cp-openfga.peristera-system.svc.cluster.local:8080"),
				OpenFGAToken: os.Getenv("OPENFGA_API_TOKEN"),
				OperatorSubs: splitList(os.Getenv("OPERATOR_SUBJECTS")),
			},
		}); err != nil {
			lg.Error(err, "adding UI/API server")
			os.Exit(1)
		}
	} else {
		lg.Info("UI/API disabled (no IAM client)")
	}
	lg.Info("starting control-plane controller")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		lg.Error(err, "manager exited")
		os.Exit(1)
	}
}
