package controller

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

var (
	certGVK     = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}
	certListGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "CertificateList"}
)

// healTenantCerts works around the external-dns / cert-manager first-issue race
// (#52): when cert-manager attempts the HTTP-01 challenge for a new tenant host
// before external-dns has published its A record, the order goes `invalid` and
// the Certificate enters cert-manager's long post-failure backoff (up to hours)
// — even though DNS resolves moments later. This sweeps the tenant's certs and
// deletes any that are stuck; ingress-shim recreates them fresh (no backoff),
// and by now the record exists, so the retry succeeds. Cloud-only (no-op when
// TLS is disabled). Runs every reconcile until the tenant settles.
func (r *TenantReconciler) healTenantCerts(ctx context.Context, tenant *v1alpha1.Tenant, ns string) {
	if !r.tlsEnabled() {
		return
	}
	lg := log.FromContext(ctx)

	// The issuer cert lives in the platform namespace; app certs in the tenant
	// namespace. Get the issuer by name, then sweep the tenant namespace.
	issuer := &unstructured.Unstructured{}
	issuer.SetGroupVersionKind(certGVK)
	if err := r.Get(ctx, client.ObjectKey{Namespace: platformNamespace, Name: "tenant-" + tenant.Spec.Slug + "-issuer-tls"}, issuer); err == nil {
		r.resetIfStuck(ctx, lg, issuer)
	}

	certs := &unstructured.UnstructuredList{}
	certs.SetGroupVersionKind(certListGVK)
	if err := r.List(ctx, certs, client.InNamespace(ns)); err != nil {
		return
	}
	for i := range certs.Items {
		r.resetIfStuck(ctx, lg, &certs.Items[i])
	}
}

func (r *TenantReconciler) resetIfStuck(ctx context.Context, lg logr.Logger, c *unstructured.Unstructured) {
	if !certStuck(c) {
		return
	}
	fails, _, _ := unstructured.NestedInt64(c.Object, "status", "failedIssuanceAttempts")
	lg.Info("resetting stuck tenant cert (external-dns race, #52)",
		"cert", c.GetName(), "namespace", c.GetNamespace(), "failures", fails)
	// Best-effort: a concurrent reconcile may have deleted it already.
	_ = r.Delete(ctx, c)
}

// certStuck reports whether a cert-manager Certificate is in post-failure
// backoff: not Ready AND cert-manager has already recorded a failed issuance
// attempt. The failure gate is what makes this safe — a cert that is merely
// still issuing for the first time has failedIssuanceAttempts == 0, so it is
// never reset (and Let's Encrypt is never hammered).
func certStuck(c *unstructured.Unstructured) bool {
	if certReady(c) {
		return false
	}
	fails, found, _ := unstructured.NestedInt64(c.Object, "status", "failedIssuanceAttempts")
	return found && fails >= 1
}

func certReady(c *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(c.Object, "status", "conditions")
	for _, raw := range conds {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}
