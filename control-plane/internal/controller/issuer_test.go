package controller

import (
	"testing"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// The OIDC issuer is decoupled from the custom domain (ADR-0021): it lives on
// the permanent <slug>.<base> host, while app hosts follow the custom domain.
// Legacy tenants (provisioned when the custom apex *was* the issuer) keep their
// issuer via status.Issuer.
func TestIssuerDecoupling(t *testing.T) {
	r := &TenantReconciler{BaseDomain: "peristera.app", URLScheme: "https", ExternalPort: "443"}

	// Default tenant — issuer and app hosts both on the slug host.
	def := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "demo"}}
	if got := r.issuerHost(def); got != "demo.peristera.app" {
		t.Errorf("issuerHost(default) = %q", got)
	}
	if got := r.tenantIssuer(def); got != "https://demo.peristera.app" {
		t.Errorf("tenantIssuer(default) = %q", got)
	}
	if got := r.instanceDomain(def); got != "demo.peristera.app" {
		t.Errorf("instanceDomain(default) = %q", got)
	}
	if got := r.tenantDomain(def); got != "demo.peristera.app" {
		t.Errorf("tenantDomain(default) = %q", got)
	}

	// New custom-domain tenant, not yet provisioned — the decoupling: the issuer
	// stays on the slug host; only app hosts move to the custom apex.
	cust := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Slug: "acme", Domain: "acme.example"}}
	if got := r.tenantIssuer(cust); got != "https://acme.peristera.app" {
		t.Errorf("tenantIssuer(new custom) = %q, want the slug host (not the custom apex)", got)
	}
	if got := r.instanceDomain(cust); got != "acme.peristera.app" {
		t.Errorf("instanceDomain(new custom) = %q", got)
	}
	if got := r.tenantDomain(cust); got != "acme.example" {
		t.Errorf("tenantDomain(new custom) = %q, want the custom apex for app hosts", got)
	}

	// Legacy tenant — status.Issuer is the source of truth, so issuer and
	// instance domain stay on the custom apex, unchanged by the decoupling.
	legacy := &v1alpha1.Tenant{
		Spec:   v1alpha1.TenantSpec{Slug: "lu", Domain: "peristera.lu"},
		Status: v1alpha1.TenantStatus{Issuer: "https://peristera.lu"},
	}
	if got := r.tenantIssuer(legacy); got != "https://peristera.lu" {
		t.Errorf("tenantIssuer(legacy) = %q, want the preserved custom-apex issuer", got)
	}
	if got := r.instanceDomain(legacy); got != "peristera.lu" {
		t.Errorf("instanceDomain(legacy) = %q", got)
	}
	if got := r.tenantDomain(legacy); got != "peristera.lu" {
		t.Errorf("tenantDomain(legacy) = %q", got)
	}

	// Migration guard: editing a legacy tenant's spec.domain must not move its
	// app hosts off the issuer apex — they stay pinned to peristera.lu.
	edited := &v1alpha1.Tenant{
		Spec:   v1alpha1.TenantSpec{Slug: "lu", Domain: "evil.example"},
		Status: v1alpha1.TenantStatus{Issuer: "https://peristera.lu"},
	}
	if got := r.tenantDomain(edited); got != "peristera.lu" {
		t.Errorf("tenantDomain(legacy, edited domain) = %q, want the pinned peristera.lu", got)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"not a url ::://":                     "",
		"https://peristera.lu":                "peristera.lu",
		"https://demo.peristera.app:443":      "demo.peristera.app",
		"http://demo.127.0.0.1.sslip.io:9080": "demo.127.0.0.1.sslip.io",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Errorf("hostOf(%q) = %q, want %q", in, got, want)
		}
	}
}
