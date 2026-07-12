package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// ensureNetworkPolicies generates the per-tenant NetworkPolicy that enforces
// the service-topology allowlist (ADR-0016). Cilium is the enforcing CNI
// (flannel does not enforce policy). The graph comes from CatalogApp.Calls;
// there is no policy data outside the catalog.
//
// Per app pod:
//   - ingress is denied except from the ingress controller (browser traffic)
//     and the sibling apps that declare this app in their Calls;
//   - egress is denied except to the same namespace (its DB, OpenFGA, and
//     declared peers), DNS, and the ingress path for its OIDC issuer.
//
// So a workload in another tenant namespace cannot reach in (cross-tenant
// isolation, #18), and a rogue app cannot reach an undeclared peer or
// exfiltrate laterally. OpenFGA additionally only accepts same-namespace
// app traffic. Create-only, like the rest of provisioning.
func (r *TenantReconciler) ensureNetworkPolicies(ctx context.Context, tenant *v1alpha1.Tenant, ns string) error {
	var policies []client.Object
	for _, app := range catalog {
		// Optional apps only get a policy when the tenant has enabled them
		// (ADR-0018); otherwise their pod does not exist. The standard shape
		// (ingress from Traefik + declared callers on the app port; egress to
		// same namespace + DNS + Traefik) fits the office engine too: it serves
		// the browser via Traefik and reaches Kamara's WOPI host in-namespace.
		if app.Optional && !tenantEnables(tenant, app.Name) {
			continue
		}
		// Ingress: the ingress controller (browser) + declared callers,
		// all on the app's port. This pins browser traffic to the Traefik
		// pod identity; it works because in kube-proxy-coexistence mode
		// Cilium sees ingressed traffic as sourced from the Traefik pod, not
		// the node (verified by the e2e "app answers on its own domain"
		// step). If a future CNI/kube-proxy change collapses that to the
		// host identity, this match breaks and browser access is denied —
		// so treat the source-identity assumption as load-bearing.
		// Only apps actually provisioned for this tenant are admitted as
		// callers (enabledCallersOf, not callersOf): a disabled optional app
		// like office is not in np-kamara's From until it is enabled, and the
		// optional-app reconcile updates this set on a toggle (R93, #47).
		from := []networkingv1.NetworkPolicyPeer{
			{NamespaceSelector: kubeSystemSelector(), PodSelector: nameSelector("traefik")},
		}
		for _, caller := range enabledCallersOf(tenant, app.Name) {
			from = append(from, networkingv1.NetworkPolicyPeer{PodSelector: nameSelector(caller)})
		}
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: policyMeta("np-"+app.Name, ns),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: *nameSelector(app.Name),
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From:  from,
					Ports: []networkingv1.NetworkPolicyPort{tcpPort(app.Port)},
				}},
				Egress: appEgress(),
			},
		})
	}

	// OpenFGA: reachable only from the same-namespace apps that actually use
	// it (NeedsOpenFGA — not every managed pod; stub has no business
	// reaching it), on its API ports. Closes the other half of #18 alongside
	// preshared-key auth.
	if anyAppNeedsOpenFGA() {
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: policyMeta("np-openfga", ns),
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: *nameSelector("openfga"),
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From:  []networkingv1.NetworkPolicyPeer{{PodSelector: openFGAConsumerSelector()}},
					Ports: []networkingv1.NetworkPolicyPort{tcpPort(8080), tcpPort(8081)},
				}},
				// Egress: its database (same namespace) + DNS.
				Egress: []networkingv1.NetworkPolicyEgressRule{
					{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
					dnsEgress(),
				},
			},
		})
	}

	for _, p := range policies {
		if err := r.createIfAbsent(ctx, tenant, p); err != nil {
			return err
		}
	}
	return nil
}

// appEgress is the allow-list every catalog app shares: same namespace (its
// DB on 5432, OpenFGA, and declared Calls peers — the app-to-app leg is
// still gated on the callee's ingress), DNS, and the ingress controller for
// the OIDC issuer/JWKS/userinfo path (the issuer host resolves to Traefik).
//
// Note: there is deliberately NO egress rule for the Kubernetes API server
// (a host IP, not a pod, so the same-namespace rule never matches it) — the
// current catalog apps are not k8s clients. A future app or sidecar that
// talks to the API server must add its own rule, or it will silently time
// out.
func appEgress() []networkingv1.NetworkPolicyEgressRule {
	return []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
		dnsEgress(),
		{To: []networkingv1.NetworkPolicyPeer{
			{NamespaceSelector: kubeSystemSelector(), PodSelector: nameSelector("traefik")},
		}},
	}
}

func dnsEgress() networkingv1.NetworkPolicyEgressRule {
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{NamespaceSelector: kubeSystemSelector(), PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"k8s-app": "kube-dns"},
			}},
		},
		Ports: []networkingv1.NetworkPolicyPort{udpPort(53), tcpPort(53)},
	}
}

func policyMeta(name, ns string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{
		"app.kubernetes.io/managed-by": "peristera-control-plane",
	}}
}

func nameSelector(app string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": app}}
}

// openFGAConsumerSelector matches the same-namespace app pods that declare
// NeedsOpenFGA — the only apps allowed to reach the tenant's OpenFGA.
func openFGAConsumerSelector() *metav1.LabelSelector {
	var names []string
	for _, a := range catalog {
		if a.NeedsOpenFGA {
			names = append(names, a.Name)
		}
	}
	return &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{
		Key:      "app.kubernetes.io/name",
		Operator: metav1.LabelSelectorOpIn,
		Values:   names,
	}}}
}

func kubeSystemSelector() *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}
}

func tcpPort(p int32) networkingv1.NetworkPolicyPort {
	proto := corev1.ProtocolTCP
	port := intstr.FromInt32(p)
	return networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &port}
}

func udpPort(p int32) networkingv1.NetworkPolicyPort {
	proto := corev1.ProtocolUDP
	port := intstr.FromInt32(p)
	return networkingv1.NetworkPolicyPort{Protocol: &proto, Port: &port}
}
