package id

import (
	"testing"
	"time"
)

func TestV7Shape(t *testing.T) {
	u := V7()
	if len(u) != 36 {
		t.Fatalf("length %d, want 36: %q", len(u), u)
	}
	for _, p := range []int{8, 13, 18, 23} {
		if u[p] != '-' {
			t.Errorf("expected '-' at %d in %q", p, u)
		}
	}
	if u[14] != '7' {
		t.Errorf("version nibble = %c, want 7 (%q)", u[14], u)
	}
	if v := u[19]; v != '8' && v != '9' && v != 'a' && v != 'b' {
		t.Errorf("variant nibble = %c, want 8-b (%q)", v, u)
	}
}

func TestV7Unique(t *testing.T) {
	seen := map[string]bool{}
	for range 10000 {
		u := V7()
		if seen[u] {
			t.Fatalf("collision: %q", u)
		}
		seen[u] = true
	}
}

func TestV7TimeOrdered(t *testing.T) {
	a := V7()
	time.Sleep(2 * time.Millisecond)
	b := V7()
	if a >= b {
		t.Errorf("not time-ordered: %q >= %q", a, b)
	}
}
