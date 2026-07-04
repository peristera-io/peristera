package engine

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"io"
	mrand "math/rand"
	"testing"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
)

func cipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	k := make([]byte, crypto.KeySize)
	if _, err := crand.Read(k); err != nil {
		t.Fatal(err)
	}
	c, err := crypto.New(k, "demo.example")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func store(t *testing.T) blob.Store {
	t.Helper()
	fs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func payload(n int, seed int64) []byte {
	b := make([]byte, n)
	mrand.New(mrand.NewSource(seed)).Read(b)
	return b
}

func readAll(rc io.ReadCloser) ([]byte, error) {
	defer rc.Close()
	return io.ReadAll(rc)
}

func TestIngestReassembleRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, st := cipher(t), store(t)
	for _, n := range []int{0, 1000, 3_000_000, 12_000_000} {
		in := payload(n, int64(n))
		refs, total, err := Ingest(ctx, bytes.NewReader(in), c, st)
		if err != nil {
			t.Fatal(err)
		}
		if total != int64(n) {
			t.Errorf("n=%d: total=%d", n, total)
		}
		var out bytes.Buffer
		if err := Reassemble(ctx, refs, c, st, &out); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(out.Bytes(), in) {
			t.Errorf("n=%d: reassembly mismatch", n)
		}
	}
}

func TestDedupAcrossObjects(t *testing.T) {
	ctx := context.Background()
	c, st := cipher(t), store(t)
	in := payload(8_000_000, 7)

	r1, _, _ := Ingest(ctx, bytes.NewReader(in), c, st)
	// Ingesting the SAME content again must reuse every chunk (no new
	// blobs) — the manifests are identical.
	r2, _, _ := Ingest(ctx, bytes.NewReader(in), c, st)
	if len(r1) != len(r2) {
		t.Fatalf("manifest lengths differ: %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("chunk %d differs across ingests", i)
		}
	}
	// The blobs are already present the second time (dedup), which
	// Reassemble proves by still decoding cleanly.
	var out bytes.Buffer
	if err := Reassemble(ctx, r2, c, st, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), in) {
		t.Error("dedup reassembly mismatch")
	}
}

func TestReassembleDetectsCorruption(t *testing.T) {
	ctx := context.Background()
	c, st := cipher(t), store(t)
	in := payload(2_000_000, 9)
	refs, _, _ := Ingest(ctx, bytes.NewReader(in), c, st)

	// Corrupt one stored blob directly.
	rc, _ := st.Get(ctx, refs[0].Hash)
	sealed, _ := readAll(rc)
	sealed[len(sealed)-1] ^= 0xff
	_ = st.Delete(ctx, refs[0].Hash)
	_ = st.Put(ctx, refs[0].Hash, bytes.NewReader(sealed))

	if err := Reassemble(ctx, refs, c, st, &bytes.Buffer{}); err == nil {
		t.Error("corrupted blob must fail reassembly (AEAD)")
	}
}
