package controller

import (
	"testing"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// In dev (no TLSIssuer) tenant ingresses stay plain http — no cert-manager
// annotation, no TLS block, and no per-tenant issuer ingress (the chart
// wildcard covers tenant hosts). On the cloud all three switch on.
func TestIngressTLSGating(t *testing.T) {
	dev := &TenantReconciler{}
	if dev.tlsEnabled() {
		t.Error("dev must not enable TLS")
	}
	if dev.ingressAnnotations("h.example") != nil {
		t.Error("dev ingress must carry no cert-manager annotation")
	}
	if dev.ingressTLS("h.example", "h-tls") != nil {
		t.Error("dev ingress must carry no TLS block")
	}

	cloud := &TenantReconciler{TLSIssuer: "letsencrypt-prod"}
	if got := cloud.ingressAnnotations("kamara.demo.peristera.app")["cert-manager.io/cluster-issuer"]; got != "letsencrypt-prod" {
		t.Errorf("cloud annotation = %q, want letsencrypt-prod", got)
	}
	tls := cloud.ingressTLS("kamara.demo.peristera.app", "kamara-tls")
	if len(tls) != 1 || tls[0].SecretName != "kamara-tls" ||
		len(tls[0].Hosts) != 1 || tls[0].Hosts[0] != "kamara.demo.peristera.app" {
		t.Errorf("cloud TLS block wrong: %+v", tls)
	}
}

// Hosts under the platform base use DNS-01; custom-domain hosts use HTTP-01
// (ADR-0021 slice 3). With no HTTP-01 issuer set, every host uses DNS-01.
func TestIssuerForHost(t *testing.T) {
	r := &TenantReconciler{BaseDomain: "peristera.app", TLSIssuer: "letsencrypt-prod", HTTP01Issuer: "letsencrypt-http01"}
	cases := map[string]string{
		"kamara.demo.peristera.app": "letsencrypt-prod",   // platform app host → DNS-01
		"demo.peristera.app":        "letsencrypt-prod",   // platform issuer host → DNS-01
		"peristera.app":             "letsencrypt-prod",   // the base itself → DNS-01
		"kamara.peristera.lu":       "letsencrypt-http01", // custom app host → HTTP-01
		"peristera.lu":              "letsencrypt-http01", // custom issuer host (legacy) → HTTP-01
	}
	for host, want := range cases {
		if got := r.issuerForHost(host); got != want {
			t.Errorf("issuerForHost(%q) = %q, want %q", host, got, want)
		}
	}
	// No HTTP-01 issuer configured → everything falls back to the DNS-01 issuer.
	noHTTP := &TenantReconciler{BaseDomain: "peristera.app", TLSIssuer: "letsencrypt-prod"}
	if got := noHTTP.issuerForHost("kamara.peristera.lu"); got != "letsencrypt-prod" {
		t.Errorf("fallback issuerForHost = %q, want letsencrypt-prod", got)
	}
}

// A tenant's public base is its custom apex when set (s4), else <slug>.<base>.
// Everything public-facing derives from this, so this is the whole switch.
func TestTenantDomain(t *testing.T) {
	r := &TenantReconciler{BaseDomain: "peristera.app"}
	def := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	if got := r.tenantDomain(def); got != "demo.peristera.app" {
		t.Errorf("default domain = %q, want demo.peristera.app", got)
	}
	custom := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "lu", Domain: "peristera.lu"}}
	if got := r.tenantDomain(custom); got != "peristera.lu" {
		t.Errorf("custom domain = %q, want peristera.lu", got)
	}
}

// The per-tenant issuer ingress must publish <slug>.<domain> with a per-host
// cert and route Login v2 + the instance to the right shared services.
func TestIssuerIngress(t *testing.T) {
	r := &TenantReconciler{BaseDomain: "peristera.app", TLSIssuer: "letsencrypt-prod"}
	tn := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	ing := r.issuerIngress(tn)

	if ing.Namespace != platformNamespace {
		t.Errorf("issuer ingress namespace = %q, want %q", ing.Namespace, platformNamespace)
	}
	if ing.Name != "tenant-demo-issuer" {
		t.Errorf("issuer ingress name = %q", ing.Name)
	}
	if ing.Annotations["cert-manager.io/cluster-issuer"] != "letsencrypt-prod" {
		t.Error("issuer ingress must carry the cert-manager annotation")
	}
	if len(ing.Spec.TLS) != 1 || ing.Spec.TLS[0].Hosts[0] != "demo.peristera.app" ||
		ing.Spec.TLS[0].SecretName != "tenant-demo-issuer-tls" {
		t.Errorf("issuer ingress TLS wrong: %+v", ing.Spec.TLS)
	}
	rule := ing.Spec.Rules[0]
	if rule.Host != "demo.peristera.app" {
		t.Errorf("issuer ingress host = %q, want demo.peristera.app", rule.Host)
	}
	paths := rule.HTTP.Paths
	if len(paths) != 2 {
		t.Fatalf("issuer ingress paths = %d, want 2", len(paths))
	}
	// Login v2 first (more specific), then the instance.
	if paths[0].Path != "/ui/v2/login" || paths[0].Backend.Service.Name != "zitadel-login" ||
		paths[0].Backend.Service.Port.Number != 3000 {
		t.Errorf("login path wrong: %+v", paths[0])
	}
	if paths[1].Path != "/" || paths[1].Backend.Service.Name != "zitadel" ||
		paths[1].Backend.Service.Port.Number != 8080 {
		t.Errorf("instance path wrong: %+v", paths[1])
	}
}
