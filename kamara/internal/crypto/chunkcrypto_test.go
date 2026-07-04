package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func key(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRoundTrip(t *testing.T) {
	c, err := New(key(t), "demo.example")
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("the quick brown fox jumps over the lazy dog")
	hash, blob, err := c.Seal(pt)
	if err != nil {
		t.Fatal(err)
	}
	if blob[0] != FormatVersion {
		t.Errorf("missing format version byte")
	}
	got, err := c.Open(hash, blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pt) {
		t.Error("round-trip mismatch")
	}
}

func TestContentAddressDedups(t *testing.T) {
	// The hash is over plaintext, so identical content → identical key
	// (dedup), independent of the random nonce making blobs differ.
	c, _ := New(key(t), "demo.example")
	pt := []byte("dedupe me")
	h1, b1, _ := c.Seal(pt)
	h2, b2, _ := c.Seal(pt)
	if h1 != h2 {
		t.Error("same content must produce the same hash")
	}
	if bytes.Equal(b1, b2) {
		t.Error("blobs should differ (random nonce) even though the hash matches")
	}
}

func TestTamperDetected(t *testing.T) {
	c, _ := New(key(t), "demo.example")
	hash, blob, _ := c.Seal([]byte("integrity"))
	blob[len(blob)-1] ^= 0xff // flip a tag byte
	if _, err := c.Open(hash, blob); err == nil {
		t.Error("tampered blob must fail to open")
	}
}

func TestWrongHashRejected(t *testing.T) {
	// Opening with a different hash changes the AD → AEAD verification
	// fails; a chunk can't be decrypted as a different chunk.
	c, _ := New(key(t), "demo.example")
	_, blob, _ := c.Seal([]byte("chunk A"))
	otherHash := ChunkHash([]byte("chunk B"))
	if _, err := c.Open(otherHash, blob); err == nil {
		t.Error("opening under the wrong hash must fail")
	}
}

func TestWrongTenantRejected(t *testing.T) {
	// The tenant is in the AD, so tenant B can't open tenant A's blob even
	// with the same key material — cross-tenant confusion defense.
	k := key(t)
	a, _ := New(k, "tenant-a.example")
	b, _ := New(k, "tenant-b.example")
	hash, blob, _ := a.Seal([]byte("secret"))
	if _, err := b.Open(hash, blob); err == nil {
		t.Error("a different tenant must not open the blob")
	}
}

func TestBadKey(t *testing.T) {
	if _, err := New(make([]byte, 16), "t"); err == nil {
		t.Error("short key must error")
	}
	if _, err := New(key(t), ""); err == nil {
		t.Error("empty tenant must error")
	}
}
