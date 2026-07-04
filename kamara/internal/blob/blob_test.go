package blob

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func TestPutGetRoundTrip(t *testing.T) {
	fs, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "deadbeef0102"
	body := []byte("blob contents")

	if err := fs.Put(ctx, key, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fs.Has(ctx, key); !ok {
		t.Error("Has should report the blob present")
	}
	rc, err := fs.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, body) {
		t.Errorf("round-trip mismatch: %q", got)
	}
}

func TestPutIsIdempotent(t *testing.T) {
	fs, _ := NewFS(t.TempDir())
	ctx := context.Background()
	key := "aabbccdd"
	if err := fs.Put(ctx, key, bytes.NewReader([]byte("first"))); err != nil {
		t.Fatal(err)
	}
	// A second Put for the same key is a content-addressed no-op — it must
	// NOT overwrite (the content is the key, so "different content" can't
	// legitimately reach the same key).
	if err := fs.Put(ctx, key, bytes.NewReader([]byte("second"))); err != nil {
		t.Fatal(err)
	}
	rc, _ := fs.Get(ctx, key)
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "first" {
		t.Errorf("idempotent Put overwrote: %q", got)
	}
}

func TestGetMissing(t *testing.T) {
	fs, _ := NewFS(t.TempDir())
	if _, err := fs.Get(context.Background(), "00112233"); err != ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	fs, _ := NewFS(t.TempDir())
	ctx := context.Background()
	_ = fs.Put(ctx, "12345678", bytes.NewReader([]byte("x")))
	if err := fs.Delete(ctx, "12345678"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := fs.Has(ctx, "12345678"); ok {
		t.Error("blob still present after delete")
	}
	// Deleting a missing blob is not an error.
	if err := fs.Delete(ctx, "12345678"); err != nil {
		t.Errorf("delete of missing blob errored: %v", err)
	}
}

func TestInvalidKeyRejected(t *testing.T) {
	fs, _ := NewFS(t.TempDir())
	ctx := context.Background()
	for _, bad := range []string{"", "ab", "../etc/passwd", "XYZ!", "abc/def"} {
		if err := fs.Put(ctx, bad, bytes.NewReader(nil)); err == nil {
			t.Errorf("invalid key %q should be rejected", bad)
		}
	}
}
