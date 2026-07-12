package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// reconcileOptionalApps converges optional-app resources to spec.apps (R93,
// #63/#47). It is a bounded exception to create-only provisioning: it tears
// down the resources of any optional app the tenant has disabled, and updates
// the always-on config that depends on an optional app's enablement (Kamara's
// office env + np-kamara's caller set). It deliberately does NOT reconcile
// general drift — only the optional dimension, driven by spec.apps.
func (r *TenantReconciler) reconcileOptionalApps(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	for _, app := range catalog {
		if !app.Optional || tenantEnables(tenant, app.Name) {
			continue
		}
		if err := r.teardownApp(ctx, ns, app.Name); err != nil {
			return err
		}
	}
	return r.reconcileKamaraOfficeWiring(ctx, tenant, ns)
}

// teardownApp deletes the resources an optional app owns when it is disabled:
// its Deployment, Service, Ingress, and NetworkPolicy. The Ingress owns its
// cert-manager Certificate (ingress-shim), so that is garbage-collected with
// it; the tls Secret is harmless if it lingers and is reused on re-enable.
// Missing objects are ignored, so this is idempotent.
func (r *TenantReconciler) teardownApp(ctx context.Context, ns, name string) error {
	objs := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "np-" + name, Namespace: ns}},
	}
	for _, o := range objs {
		if err := r.Delete(ctx, o); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// reconcileKamaraOfficeWiring keeps Kamara in sync with the office toggle. Both
// touched objects are create-only in ensureApps, so on a toggle we update them
// here: np-kamara's admitted callers (office added on enable, removed on
// disable) and Kamara's OFFICE_URL/WOPI_SRC_BASE env (whose change rolls the
// pod so it re-reads WOPI discovery — R94). Each is a no-op before Kamara
// exists and when already in the desired state (so no needless pod churn).
func (r *TenantReconciler) reconcileKamaraOfficeWiring(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	if err := r.reconcileKamaraCallers(ctx, tenant, ns); err != nil {
		return err
	}
	return r.reconcileKamaraEnv(ctx, tenant, ns)
}

func (r *TenantReconciler) reconcileKamaraCallers(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	np := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, client.ObjectKey{Name: "np-kamara", Namespace: ns}, np)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(np.Spec.Ingress) != 1 {
		// ensureNetworkPolicies always emits exactly one ingress rule; an
		// unexpected shape means something else edited the policy — don't
		// silently converge it away.
		log.FromContext(ctx).V(1).Info("np-kamara has unexpected ingress shape; skipping caller reconcile",
			"rules", len(np.Spec.Ingress), "namespace", ns)
		return nil
	}
	desired := enabledCallersOf(tenant, "kamara")
	if equalStringSet(callerNames(np.Spec.Ingress[0].From), desired) {
		return nil
	}
	from := []networkingv1.NetworkPolicyPeer{
		{NamespaceSelector: kubeSystemSelector(), PodSelector: nameSelector("traefik")},
	}
	for _, caller := range desired {
		from = append(from, networkingv1.NetworkPolicyPeer{PodSelector: nameSelector(caller)})
	}
	np.Spec.Ingress[0].From = from
	return r.Update(ctx, np)
}

func (r *TenantReconciler) reconcileKamaraEnv(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	dep := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKey{Name: "kamara", Namespace: ns}, dep)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil || len(dep.Spec.Template.Spec.Containers) == 0 {
		return err
	}
	c := &dep.Spec.Template.Spec.Containers[0]
	desired := append(withoutOfficeEnv(c.Env), r.officeEnv(tenant, ns)...)
	if equalEnv(c.Env, desired) {
		return nil
	}
	c.Env = desired
	return r.Update(ctx, dep)
}

// callerNames extracts the app names a NetworkPolicy admits as callers (the
// pod-selected peers), ignoring the ingress-controller peer.
func callerNames(peers []networkingv1.NetworkPolicyPeer) []string {
	var out []string
	for _, p := range peers {
		if p.NamespaceSelector != nil || p.PodSelector == nil {
			continue // the Traefik peer (namespace-scoped) or a non-pod peer
		}
		if name := p.PodSelector.MatchLabels["app.kubernetes.io/name"]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

// withoutOfficeEnv returns env with the office-embed vars removed, preserving
// the order of the rest (they are appended last in ensureApps, so stripping +
// re-appending is a no-op in steady state).
func withoutOfficeEnv(env []corev1.EnvVar) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(env))
	for _, e := range env {
		if e.Name == "OFFICE_URL" || e.Name == "WOPI_SRC_BASE" {
			continue
		}
		out = append(out, e)
	}
	return out
}

// equalEnv compares env vars by Name+Value only. That is sufficient here
// because this path mutates exactly the two plain-Value office vars; the
// ValueFrom vars (DATABASE_DSN, OPENFGA token, DEK) are copied through
// untouched, so their refs never change between the current and desired slice.
func equalEnv(a, b []corev1.EnvVar) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Value != b[i].Value {
			return false
		}
	}
	return true
}

// equalStringSet reports whether a and b contain the same distinct elements,
// ignoring order and duplicates.
func equalStringSet(a, b []string) bool {
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, s := range a {
		sa[s] = true
	}
	for _, s := range b {
		sb[s] = true
	}
	if len(sa) != len(sb) {
		return false
	}
	for s := range sa {
		if !sb[s] {
			return false
		}
	}
	return true
}
