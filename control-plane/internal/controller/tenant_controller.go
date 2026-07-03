// Package controller reconciles Tenant resources: it converges the cluster
// to each Tenant's spec (ADR-0008). M2 session 2 scope: namespace + CNPG
// Postgres. The Zitadel virtual instance (ADR-0006 §6) attaches next; its
// external cleanup is why the finalizer exists already.
package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	// LoginDomain is the deployment's ExternalDomain — every new
	// instance must trust it or the shared Login v2 cannot serve it.
	LoginDomain string
}

func (r *TenantReconciler) tenantDomain(t *v1alpha1.Tenant) string {
	return t.Spec.Slug + "." + r.BaseDomain
}

func (r *TenantReconciler) tenantIssuer(t *v1alpha1.Tenant) string {
	// http is dev-grade; the scheme becomes config with the first TLS
	// environment (M6).
	return fmt.Sprintf("http://%s:%s", r.tenantDomain(t), r.ExternalPort)
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
		// the tenant's Zitadel virtual instance.
		if r.IAM != nil && tenant.Status.InstanceID != "" {
			err := r.IAM.DeleteInstance(ctx, tenant.Status.InstanceID)
			switch {
			case errors.Is(err, zitadel.ErrNotFound):
				// Already gone. (A fresh instance 404s for a few seconds
				// after creation — acceptable here: deletion this close
				// to creation retries via the error path below first.)
			case err != nil:
				lg.Error(err, "deleting zitadel instance", "instanceId", tenant.Status.InstanceID)
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
		if iamReady {
			// Apps need the issuer + clientId; the initial admin needs
			// the instance to be serving. Both are idempotent.
			if err := r.ensureApps(ctx, tenant, nsName); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.ensureInitialAdmin(ctx, tenant, nsName); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	phase := v1alpha1.TenantPending
	if dbReady && iamReady {
		phase = v1alpha1.TenantReady
	}
	setCondition(tenant, "DatabaseReady", dbReady)
	setCondition(tenant, "IAMProvisioned", iamReady)
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
	appBase := fmt.Sprintf("http://stub.%s:%s", domain, r.ExternalPort)
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

func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	db := &unstructured.Unstructured{}
	db.SetGroupVersionKind(cnpgGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tenant{}).
		Owns(&corev1.Namespace{}).
		Owns(db).
		Complete(r)
}
