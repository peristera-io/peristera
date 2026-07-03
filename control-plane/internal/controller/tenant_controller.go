// Package controller reconciles Tenant resources: it converges the cluster
// to each Tenant's spec (ADR-0008). M2 session 2 scope: namespace + CNPG
// Postgres. The Zitadel virtual instance (ADR-0006 §6) attaches next; its
// external cleanup is why the finalizer exists already.
package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

const finalizer = "peristera.io/tenant"

var cnpgGVK = schema.GroupVersionKind{
	Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster",
}

type TenantReconciler struct {
	client.Client
}

func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lg := log.FromContext(ctx)

	tenant := &v1alpha1.Tenant{}
	if err := r.Get(ctx, req.NamespacedName, tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tenant.DeletionTimestamp.IsZero() {
		// k8s garbage collection removes the namespace (owner reference);
		// this hook is for cleanup outside the cluster — the tenant's
		// Zitadel virtual instance, once session 3 wires it in.
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

	phase := v1alpha1.TenantPending
	if ready, err := r.databaseReady(ctx, nsName); err != nil {
		return ctrl.Result{}, err
	} else if ready {
		phase = v1alpha1.TenantReady
	}
	if tenant.Status.Phase != phase {
		tenant.Status.Phase = phase
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		lg.Info("tenant phase", "tenant", tenant.Name, "phase", phase)
	}
	return ctrl.Result{}, nil
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
