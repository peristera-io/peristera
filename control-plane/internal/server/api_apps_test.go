package server

import "testing"

// validateOptionalApps accepts only optional catalog apps, dedupes, and treats
// an empty list as "disable everything". Always-on apps (kamara) and unknown
// names are rejected so a toggle can't provision an arbitrary workload.
func TestValidateOptionalApps(t *testing.T) {
	ok, err := validateOptionalApps([]string{"office", "office"})
	if err != nil || len(ok) != 1 || ok[0] != "office" {
		t.Errorf("office+dupe = (%v, %v), want ([office], nil)", ok, err)
	}
	empty, err := validateOptionalApps(nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("empty = (%v, %v), want ([], nil)", empty, err)
	}
	if _, err := validateOptionalApps([]string{"kamara"}); err == nil {
		t.Error("always-on app kamara must be rejected as non-optional")
	}
	if _, err := validateOptionalApps([]string{"bogus"}); err == nil {
		t.Error("unknown app must be rejected")
	}
}
