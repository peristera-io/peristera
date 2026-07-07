package controller

import (
	"testing"

	"github.com/peristera-io/peristera/control-plane/apis/v1alpha1"
)

// The office engine must be opt-in (Optional) and provisioned off the standard
// path (External) — the invariants ensureApps/ensureNetworkPolicies rely on.
func TestOfficeCatalogEntry(t *testing.T) {
	var office *CatalogApp
	for i := range catalog {
		if catalog[i].Name == "office" {
			office = &catalog[i]
		}
	}
	if office == nil {
		t.Fatal("office engine missing from the catalog")
	}
	if !office.Optional {
		t.Error("office must be Optional (opt-in per tenant, ADR-0018)")
	}
	if !office.External {
		t.Error("office must be External (provisioned by ensureOffice, not the standard path)")
	}
}

func TestTenantEnables(t *testing.T) {
	tn := &v1alpha1.Tenant{Spec: v1alpha1.TenantSpec{Apps: []string{"office"}}}
	if !tenantEnables(tn, "office") {
		t.Error("tenant with office in Spec.Apps should enable office")
	}
	if tenantEnables(tn, "other") {
		t.Error("apps not in Spec.Apps must not be enabled")
	}
	if tenantEnables(&v1alpha1.Tenant{}, "office") {
		t.Error("empty Spec.Apps must enable nothing")
	}
}

// office declares Calls:[kamara], so kamara's NetworkPolicy must admit office
// as a caller in every tenant — the editor→WOPI-host edge (ADR-0018). It is
// platform-uniform (independent of per-tenant opt-in): when office is not
// enabled its pod selector matches nothing, so admitting it is harmless.
func TestKamaraAdmitsOfficeCaller(t *testing.T) {
	callers := callersOf("kamara")
	var hasOffice, hasErgonomos bool
	for _, c := range callers {
		switch c {
		case "office":
			hasOffice = true
		case "ergonomos":
			hasErgonomos = true
		}
	}
	if !hasOffice {
		t.Error("kamara must admit office as a WOPI caller")
	}
	if !hasErgonomos {
		t.Error("kamara must still admit ergonomos (regression guard)")
	}
}
