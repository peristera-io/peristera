package pii

import (
	"context"
	"sync"
	"testing"
)

func TestPseudonymStableAndErasable(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryPseudonyms()
	alice := Subject{Instance: "demo.example", UserID: "alice"}
	bob := Subject{Instance: "demo.example", UserID: "bob"}

	tok1, err := p.TokenFor(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	// Stable: same subject → same token.
	tok2, _ := p.TokenFor(ctx, alice)
	if tok1 != tok2 {
		t.Errorf("token not stable: %q vs %q", tok1, tok2)
	}
	// Distinct subjects → distinct tokens.
	tokBob, _ := p.TokenFor(ctx, bob)
	if tokBob == tok1 {
		t.Error("distinct subjects share a token")
	}
	// Token leaks nothing: it must not contain the user id.
	if len(tok1) == 0 || contains(tok1, "alice") {
		t.Errorf("token %q reveals the subject", tok1)
	}

	// Resolvable before erasure.
	if got, ok, _ := p.Resolve(ctx, tok1); !ok || got != alice {
		t.Errorf("Resolve before erase = %v,%v", got, ok)
	}
	// Erasure breaks linkability for alice only.
	if err := p.Erase(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Resolve(ctx, tok1); ok {
		t.Error("alice still resolvable after erasure")
	}
	if _, ok, _ := p.Resolve(ctx, tokBob); !ok {
		t.Error("bob wrongly affected by alice's erasure")
	}
	// Erase is idempotent.
	if err := p.Erase(ctx, alice); err != nil {
		t.Errorf("second erase errored: %v", err)
	}
}

// TestTokenForConcurrent exercises the allocation race: many goroutines
// request a token for the same subject at once; all must converge on one
// token (run with -race). This is the one non-trivial concurrency claim.
func TestTokenForConcurrent(t *testing.T) {
	ctx := context.Background()
	p := NewInMemoryPseudonyms()
	subj := Subject{Instance: "demo.example", UserID: "racer"}

	const n = 50
	tokens := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := p.TokenFor(ctx, subj)
			if err != nil {
				t.Errorf("TokenFor: %v", err)
			}
			tokens[i] = tok
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if tokens[i] != tokens[0] || tokens[i] == "" {
			t.Fatalf("concurrent TokenFor diverged: %q vs %q", tokens[i], tokens[0])
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
