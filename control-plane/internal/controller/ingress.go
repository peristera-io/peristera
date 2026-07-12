package controller

import (
	"context"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// platformNamespace is where the shared platform services (Zitadel and its
// Login v2 app) live — the same in dev and on the cloud, matching the
// hardcoded cp-openfga/service assumptions elsewhere.
const platformNamespace = "peristera-system"

// tlsEnabled reports whether tenant ingresses should carry real per-host certs
// (M7 s2, cloud). An empty issuer means dev: Traefik serves plain http and
// Zitadel's chart *.<domain> wildcard ingress already covers tenant hosts, so
// no per-tenant ingress/cert is needed (and HTTP-01 can't issue a wildcard).
func (r *TenantReconciler) tlsEnabled() bool { return r.TLSIssuer != "" }

// issuerForHost picks the cert-manager ClusterIssuer for a host (ADR-0021).
// Hosts under the platform base resolve in our Scaleway DNS zone, so they use
// the DNS-01 issuer (no external-dns first-issue race, #52). Custom-domain
// hosts live in a zone we don't control and can't solve DNS-01 without CNAME
// delegation, so they use HTTP-01 — safe because the customer's A record is
// already live (they point it at us before provisioning), so there is no race.
// Falls back to the DNS-01 issuer when no separate HTTP-01 issuer is set.
func (r *TenantReconciler) issuerForHost(host string) string {
	if r.HTTP01Issuer == "" || host == r.BaseDomain || strings.HasSuffix(host, "."+r.BaseDomain) {
		return r.TLSIssuer
	}
	return r.HTTP01Issuer
}

// ingressAnnotations returns the cert-manager cluster-issuer annotation so
// cert-manager issues a per-host Let's Encrypt cert for the ingress (ADR-0020),
// choosing the issuer by host (issuerForHost); nil in dev.
func (r *TenantReconciler) ingressAnnotations(host string) map[string]string {
	if !r.tlsEnabled() {
		return nil
	}
	return map[string]string{"cert-manager.io/cluster-issuer": r.issuerForHost(host)}
}

// ingressTLS returns the per-host TLS block (cert stored in secretName) when
// TLS is enabled; nil in dev.
func (r *TenantReconciler) ingressTLS(host, secretName string) []networkingv1.IngressTLS {
	if !r.tlsEnabled() {
		return nil
	}
	return []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: secretName}}
}

// issuerIngress builds the ingress that publishes a tenant's Zitadel
// virtual-instance issuer host (<slug>.<domain>) with its own per-host cert.
// It routes like the shared platform ingress does — Login v2 first, then the
// instance — but for this one tenant host. Pure builder (no client), so the
// routing/TLS shape is unit-testable.
func (r *TenantReconciler) issuerIngress(tenant *v1alpha1.Tenant) *networkingv1.Ingress {
	// The issuer is served on the permanent issuer host (ADR-0021), which is
	// decoupled from the app domain; for legacy tenants instanceDomain resolves
	// to their original custom apex via the status.Issuer override.
	host := r.instanceDomain(tenant)
	pathType := networkingv1.PathTypePrefix
	ingressClass := "traefik"
	backend := func(svc string, port int32) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
			Name: svc, Port: networkingv1.ServiceBackendPort{Number: port},
		}}
	}
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tenant-" + tenant.Spec.Slug + "-issuer",
			Namespace: platformNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "peristera-control-plane",
				"peristera.io/tenant":          tenant.Name,
			},
			Annotations: r.ingressAnnotations(host),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClass,
			TLS:              r.ingressTLS(host, "tenant-"+tenant.Spec.Slug+"-issuer-tls"),
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							// Login v2 is the more specific route; the instance
							// serves everything else (OIDC, console, APIs).
							{Path: "/ui/v2/login", PathType: &pathType, Backend: backend("zitadel-login", 3000)},
							{Path: "/", PathType: &pathType, Backend: backend("zitadel", 8080)},
						},
					},
				},
			}},
		},
	}
}

// ensureIssuerIngress publishes the tenant issuer host over real TLS (cloud
// only). It lives in the platform namespace (where the shared Zitadel + login
// services are) and is owned by the cluster-scoped Tenant, so it is GC'd on
// off-boarding. No-op in dev (the wildcard chart ingress covers tenant hosts).
func (r *TenantReconciler) ensureIssuerIngress(ctx context.Context, tenant *v1alpha1.Tenant) error {
	if !r.tlsEnabled() {
		return nil
	}
	return r.createIfAbsent(ctx, tenant, r.issuerIngress(tenant))
}
