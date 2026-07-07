package v1alpha1

import "testing"

func TestValidSlug(t *testing.T) {
	valid := []string{"demo", "a", "demo-gmbh", "t42", "0x"}
	invalid := []string{"", "-demo", "demo-", "Demo", "de_mo", "dé", "a.b",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} // 64 chars

	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) = true, want false", s)
		}
	}
}

func TestValidDomain(t *testing.T) {
	valid := []string{"peristera.lu", "acme.example.com", "a.io", "sub.acme-gmbh.de"}
	invalid := []string{"", "peristera", "Peristera.lu", "peristera.", ".lu",
		"peristera.l", "acme_corp.com", "http://acme.com", "acme.com/x", "a..b"}

	for _, s := range valid {
		if !ValidDomain(s) {
			t.Errorf("ValidDomain(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidDomain(s) {
			t.Errorf("ValidDomain(%q) = true, want false", s)
		}
	}
}
