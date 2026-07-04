package chunk

import (
	"bytes"
	"math/rand"
	"testing"
)

// deterministic pseudo-random data (fixed seed, no global rand).
func data(n int, seed int64) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewSource(seed))
	r.Read(b)
	return b
}

func splitAll(t *testing.T, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	err := Split(bytes.NewReader(b), func(c []byte) error {
		cp := make([]byte, len(c))
		copy(cp, c)
		out = append(out, cp)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReassembly(t *testing.T) {
	for _, n := range []int{0, 100, MinSize - 1, MinSize + 1, AvgSize, 5 * MaxSize, 10_000_000} {
		chunks := splitAll(t, data(n, 1))
		var got []byte
		for _, c := range chunks {
			got = append(got, c...)
		}
		if len(got) != n {
			t.Fatalf("n=%d: reassembled %d bytes", n, len(got))
		}
	}
}

func TestSizeBounds(t *testing.T) {
	chunks := splitAll(t, data(20_000_000, 2))
	if len(chunks) < 2 {
		t.Fatal("expected many chunks")
	}
	for i, c := range chunks {
		last := i == len(chunks)-1
		if !last && len(c) < MinSize {
			t.Errorf("chunk %d below MinSize: %d", i, len(c))
		}
		if len(c) > MaxSize {
			t.Errorf("chunk %d above MaxSize: %d", i, len(c))
		}
	}
}

func TestDeterministic(t *testing.T) {
	b := data(8_000_000, 3)
	a1 := splitAll(t, b)
	a2 := splitAll(t, b)
	if len(a1) != len(a2) {
		t.Fatalf("nondeterministic chunk count: %d vs %d", len(a1), len(a2))
	}
	for i := range a1 {
		if !bytes.Equal(a1[i], a2[i]) {
			t.Fatalf("chunk %d differs across runs", i)
		}
	}
}

// TestInsertionIsLocal is the property that justifies content-defined
// chunking: inserting bytes near the start changes only nearby chunks, so
// most chunks (and their hashes) are reused across versions.
func TestInsertionIsLocal(t *testing.T) {
	base := data(8_000_000, 4)
	v1 := splitAll(t, base)

	// Insert 10 bytes near the front.
	edited := append([]byte{}, base[:1000]...)
	edited = append(edited, []byte("XXXXXXXXXX")...)
	edited = append(edited, base[1000:]...)
	v2 := splitAll(t, edited)

	// Count chunks common to both (by content) — most should survive.
	set := map[string]bool{}
	for _, c := range v1 {
		set[string(c)] = true
	}
	shared := 0
	for _, c := range v2 {
		if set[string(c)] {
			shared++
		}
	}
	if shared < len(v2)/2 {
		t.Errorf("insertion reshuffled too much: only %d/%d chunks reused", shared, len(v2))
	}
}
