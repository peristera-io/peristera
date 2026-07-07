// Package controller reconciles Tenant resources: it converges the cluster
// to each Tenant's spec (ADR-0008). M2 session 2 scope: namespace + CNPG
// Postgres. The Zitadel virtual instance (ADR-0006 §6) attaches next; its
// external cleanup is why the finalizer exists already.
package controller

import (
	"context"
	"errors"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
	"github.com/peristera-io/peristera/control-plane/internal/zitadel"
)

const finalizer = "peristera.io/tenant"

var cnpgGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster",
}

type TenantReconciler struct {
	client.Client
	// IAM provisioning (ADR-0006 §6); nil disables it (unit tests, CI).
	IAM *zitadel.Client
	// BaseDomain is the suffix under which tenant domains live
	// (dev: 127.0.0.1.sslip.io); ExternalPort its public port.
	BaseDomain   string
	ExternalPort string
	// ImagePrefix + ImageTag resolve catalog app images (M7 s0): dev
	// "peristera-" / "dev" (k3d-imported), cloud "ghcr.io/peristera-io/" /
	// "<version>" (pulled).
	ImagePrefix string
	ImageTag    string
	// Scheme of tenant public URLs (M7 s1): "http" in dev, "https" on the
	// cloud with real certs. The tenant issuer is a public URL and must equal
	// the token `iss`, so this is load-bearing for OIDC.
	URLScheme string
	// TLSIssuer is the cert-manager ClusterIssuer name for per-host tenant
	// certs (M7 s2): "letsencrypt-prod" on the cloud, empty in dev (Traefik
	// serves plain http and Zitadel's wildcard ingress covers tenant hosts).
	// When set, tenant issuer + app ingresses get a cert-manager annotation +
	// TLS block, and the tenant issuer host gets its own ingress.
	TLSIssuer string
	// LoginDomain is the deployment's ExternalDomain — every new
	// instance must trust it or the shared Login v2 cannot serve it.
	LoginDomain string
}

func (r *TenantReconciler) tenantDomain(t *v1alpha1.Tenant) string {
	return t.Spec.Slug + "." + r.BaseDomain
}

// publicURL builds a tenant-facing URL for host under the configured scheme,
// omitting the port when it is the scheme's default (443 for https, 80 for
// http) — so cloud tenants are clean `https://<host>` and dev keeps
// `http://<host>:9080` (M7 s1).
func (r *TenantReconciler) publicURL(host string) string {
	scheme := r.URLScheme
	if scheme == "" {
		scheme = "http"
	}
	port := r.ExternalPort
	if port == "" || (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

func (r *TenantReconciler) tenantIssuer(t *v1alpha1.Tenant) string {
	return r.publicURL(r.tenantDomain(t))
}

func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		// k8s garbage collection removes the namespace (owner reference);
		// this hook removes what lives outside the cluster's GC reach:
		// the tenant's Zitadel virtual instance. Off-boarding is GDPR
		// posture — a leaked instance holds tenant identity data, so we
		// must not remove the finalizer until the instance is really gone.
		if r.IAM != nil {
			gone, err := r.deleteInstance(ctx, tenant)
			if err != nil {
				lg.Error(err, "deleting zitadel instance", "tenant", tenant.Name)
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
			if !gone {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
		}
		if controllerutil.RemoveFinalizer(tenant, finalizer) {
			return ctrl.Result{}, r.Update(ctx, tenant)
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(tenant, finalizer) {
		if err := r.Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
	}

	nsName := "tenant-" + tenant.Spec.Slug
	if err := r.ensureNamespace(ctx, tenant, nsName); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureDatabase(ctx, tenant, nsName); err != nil {
		return ctrl.Result{}, err
	}

	dbReady, err := r.databaseReady(ctx, nsName)
	if err != nil {
		return ctrl.Result{}, err
	}

	iamReady := r.IAM == nil // without an IAM client, IAM isn't part of Ready
	var requeue time.Duration
	if r.IAM != nil {
		iamReady, requeue, err = r.provisionIAM(ctx, tenant)
		if err != nil {
			return ctrl.Result{}, err
		}
		// Apps need the issuer + clientId (IAM) AND the cluster's app
		// credentials (dbReady) to assemble per-app DSNs — gate on both so
		// a fresh tenant whose Postgres is still bootstrapping cleanly
		// requeues instead of erroring on the missing db-app secret.
		if iamReady && dbReady {
			if err := r.ensureApps(ctx, tenant, nsName); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.ensureInitialAdmin(ctx, tenant, nsName); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// A tenant is only Ready once its app workloads are actually up — not the
	// moment their manifests are created (#31). Owning Deployments makes an
	// availability change re-trigger reconcile; the timed requeue below is a
	// backstop until the first pod reports.
	appsReady := r.IAM == nil // without IAM, apps aren't provisioned — don't gate on them
	if r.IAM != nil && iamReady && dbReady {
		appsReady, err = r.appsReady(ctx, tenant, nsName)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	phase := v1alpha1.TenantPending
	if dbReady && iamReady && appsReady {
		phase = v1alpha1.TenantReady
	} else if requeue == 0 {
		requeue = 10 * time.Second // re-check workloads that are still coming up
	}
	setCondition(tenant, "DatabaseReady", dbReady)
	setCondition(tenant, "IAMProvisioned", iamReady)
	setCondition(tenant, "AppsReady", appsReady)
	if tenant.Status.Phase != phase {
		tenant.Status.Phase = phase
		lg.Info("tenant phase", "tenant", tenant.Name, "phase", phase)
	}
	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// provisionIAM walks the ADR-0006 §6 sequence, one durable step per
// reconcile: instance → (instance serving) → trusted domain + project +
// PKCE app. status.instanceId / status.clientId are the idempotency
// records. Returns done, requeue-hint, error.
func (r *TenantReconciler) provisionIAM(ctx context.Context, tenant *v1alpha1.Tenant) (bool, time.Duration, error) {
	lg := log.FromContext(ctx)
	domain, issuer := r.tenantDomain(tenant), r.tenantIssuer(tenant)

	// Publish the issuer host over real TLS (cloud) before anything tries to
	// reach it: DiscoveryAlive below polls https://<issuer>, which only answers
	// once this ingress exists, external-dns has a record, and cert-manager has
	// issued the per-host cert. No-op in dev.
	if err := r.ensureIssuerIngress(ctx, tenant); err != nil {
		return false, 0, err
	}

	if tenant.Status.InstanceID == "" {
		id, err := r.IAM.InstanceIDByDomain(ctx, domain) // adopt strays
		if errors.Is(err, zitadel.ErrNotFound) {
			id, err = r.IAM.CreateInstance(ctx, tenant.Spec.Slug, domain, orgName(tenant))
			lg.Info("created zitadel instance", "tenant", tenant.Name, "instanceId", id)
		}
		if err != nil {
			return false, 0, err
		}
		tenant.Status.InstanceID = id
		return false, 3 * time.Second, nil // persist the ID before moving on
	}

	if tenant.Status.ClientID != "" {
		return true, 0, nil
	}

	if !r.IAM.DiscoveryAlive(ctx, issuer) {
		return false, 3 * time.Second, nil // fresh instance, projections lag
	}
	if err := r.IAM.AddTrustedDomain(ctx, issuer, tenant.Status.InstanceID, r.LoginDomain); err != nil {
		return false, 0, err
	}
	orgID, err := r.IAM.FirstOrgID(ctx, issuer)
	if err != nil {
		return false, 0, err
	}
	appBase := r.publicURL("stub." + domain)
	clientID, err := r.IAM.EnsureStubApp(ctx, issuer, orgID,
		[]string{appBase + "/auth/callback"}, []string{appBase + "/"})
	if err != nil {
		return false, 0, err
	}
	tenant.Status.Issuer = issuer
	tenant.Status.ClientID = clientID
	lg.Info("tenant IAM provisioned", "tenant", tenant.Name, "issuer", issuer, "clientId", clientID)
	return true, 0, nil
}

// deleteInstance removes the tenant's Zitadel virtual instance, returning
// whether it is confirmed gone. It is safe to call repeatedly (the
// finalizer requeues until gone==true).
//
// Two hazards ADR-0006 calls out are handled here:
//  1. status.InstanceID may never have been persisted (a status-update
//     failure between CreateInstance and its Update). Fall back to finding
//     the instance by its domain so a stray isn't orphaned.
//  2. A just-created instance 404s on the System API for a few seconds
//     (projection lag). A 404 therefore does NOT prove the instance is
//     gone — only that the System API can't see it yet. We trust it only
//     when the instance's own OIDC discovery has also stopped answering;
//     otherwise we report not-gone and let the caller requeue.
func (r *TenantReconciler) deleteInstance(ctx context.Context, tenant *v1alpha1.Tenant) (gone bool, err error) {
	id := tenant.Status.InstanceID
	if id == "" {
		id, err = r.IAM.InstanceIDByDomain(ctx, r.tenantDomain(tenant))
		if errors.Is(err, zitadel.ErrNotFound) {
			return true, nil // never created, or already deleted
		}
		if err != nil {
			return false, err
		}
	}

	err = r.IAM.DeleteInstance(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, zitadel.ErrNotFound):
		// Genuinely gone only if the instance is no longer serving; a live
		// issuer means the 404 is projection lag, so keep requeueing.
		return !r.IAM.DiscoveryAlive(ctx, r.tenantIssuer(tenant)), nil
	default:
		return false, err
	}
}

func orgName(t *v1alpha1.Tenant) string {
	if t.Spec.DisplayName != "" {
		return t.Spec.DisplayName
	}
	return t.Spec.Slug
}

func setCondition(t *v1alpha1.Tenant, kind string, ok bool) {
	status := metav1.ConditionFalse
	reason := "Pending"
	if ok {
		status, reason = metav1.ConditionTrue, "Done"
	}
	meta.SetStatusCondition(&t.Status.Conditions, metav1.Condition{
		Type: kind, Status: status, Reason: reason,
		ObservedGeneration: t.Generation,
	})
}

func (r *TenantReconciler) ensureNamespace(ctx context.Context, tenant *v1alpha1.Tenant, name string) error {
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: name}, ns)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	ns = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: name,
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "peristera-control-plane",
			"peristera.io/tenant":          tenant.Name,
		},
	}}
	if err := controllerutil.SetControllerReference(tenant, ns, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, ns)
}

func (r *TenantReconciler) ensureDatabase(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	db := &unstructured.Unstructured{}
	db.SetGroupVersionKind(cnpgGVK)
	err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: "db"}, db)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	db = &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "postgresql.cnpg.io/v1",
		"kind":       "Cluster",
		"metadata":   map[string]any{"name": "db", "namespace": ns},
		"spec": map[string]any{
			// Dev sizing; opinionated production defaults are an M6 concern.
			"instances": int64(1),
			"storage":   map[string]any{"size": "1Gi"},
			// Bounded, fast shutdown so tenant off-boarding (namespace
			// delete) isn't held for CNPG's 30-minute default stopDelay:
			// during namespace teardown the instance manager loses its API
			// credentials and won't exit cleanly, riding out the whole grace
			// period. stopDelay caps the pod's terminationGracePeriod;
			// smartShutdownTimeout drains connections briefly first. Off-
			// boarding must be prompt (GDPR posture); revisit for production
			// HA (M6).
			"smartShutdownTimeout": int64(10),
			"stopDelay":            int64(30),
		},
	}}
	db.SetGroupVersionKind(cnpgGVK)
	if err := controllerutil.SetControllerReference(tenant, db, r.Scheme()); err != nil {
		return err
	}
	return r.Create(ctx, db)
}

func (r *TenantReconciler) databaseReady(ctx context.Context, ns string) (bool, error) {
	db := &unstructured.Unstructured{}
	db.SetGroupVersionKind(cnpgGVK)
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: "db"}, db); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	ready, found, err := unstructured.NestedInt64(db.Object, "status", "readyInstances")
	if err != nil || !found {
		return false, nil
	}
	return ready >= 1, nil
}

// appsReady reports whether every catalog app that should run for this tenant
// has an available Deployment (#31). An optional app the tenant hasn't enabled
// is skipped; a not-yet-created or zero-available Deployment means not ready.
func (r *TenantReconciler) appsReady(ctx context.Context, tenant *v1alpha1.Tenant, ns string) (bool, error) {
	for _, app := range catalog {
		if app.Optional && !tenantEnables(tenant, app.Name) {
			continue
		}
		var d appsv1.Deployment
		if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: app.Name}, &d); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		if d.Status.AvailableReplicas < 1 {
			return false, nil
		}
	}
	return true, nil
}

func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	db := &unstructured.Unstructured{}
	db.SetGroupVersionKind(cnpgGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{}).
		Owns(&corev1.Namespace{}).
		Owns(db).
		Owns(&appsv1.Deployment{}). // app availability drives Ready (#31)
		Complete(r)
}
