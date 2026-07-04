package pii

import "testing"

func TestParseSubjectRoundTrip(t *testing.T) {
	valid := []string{
		"demo.127.0.0.1.sslip.io/2789",
		"a.example/u",
		"tenant.peristera.io/380022847386419223",
	}
	for _, s := range valid {
		subj, err := ParseSubject(s)
		if err != nil {
			t.Fatalf("ParseSubject(%q) errored: %v", s, err)
		}
		if got := subj.String(); got != s {
			t.Errorf("round-trip %q → %q", s, got)
		}
	}

	invalid := []string{"", "noslash", "/no-instance", "no-user/", "/"}
	for _, s := range invalid {
		if _, err := ParseSubject(s); err == nil {
			t.Errorf("ParseSubject(%q) = nil error, want error", s)
		}
	}
}

func TestSubjectOpenFGAObject(t *testing.T) {
	s := Subject{Instance: "demo.example", UserID: "42"}
	if got, want := s.OpenFGAObject(), "user:demo.example/42"; got != want {
		t.Errorf("OpenFGAObject() = %q, want %q", got, want)
	}
	if !(Subject{}).Zero() || s.Zero() {
		t.Error("Zero() wrong")
	}
}
