// Package crypto implements Kamara's at-rest chunk format (ADR-0001
// §3,§6,§7): content-addressing by BLAKE3(plaintext), and encrypt-once
// under a per-tenant data-encryption key with a random nonce in the blob
// header and a content-scoped associated-data binding. The format is
// E2EE-ready — the version byte and named algorithm let a later format
// change be additive.
package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
	"lukechampine.com/blake3"
)

// FormatVersion is the leading byte of every stored blob (ADR-0001 §3).
const FormatVersion byte = 0x01

// adDomain namespaces the associated data so a chunk blob can't be
// reinterpreted in another context.
const adDomain = "peristera.kamara.v1.chunk"

// KeySize is the per-tenant DEK length (XChaCha20-Poly1305).
const KeySize = chacha20poly1305.KeySize // 32

// Cipher seals and opens chunk blobs for one tenant.
type Cipher struct {
	aead   cipher.AEAD
	tenant string
}

// New builds a Cipher from a tenant's 32-byte DEK.
func New(dek []byte, tenant string) (*Cipher, error) {
	if len(dek) != KeySize {
		return nil, fmt.Errorf("crypto: DEK must be %d bytes, got %d", KeySize, len(dek))
	}
	if tenant == "" {
		return nil, fmt.Errorf("crypto: tenant required")
	}
	aead, err := chacha20poly1305.NewX(dek)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead, tenant: tenant}, nil
}

// ChunkHash is the content address of a chunk — BLAKE3 of its plaintext,
// hex-encoded. Identical content dedups to one stored blob (ADR-0001 §2).
func ChunkHash(plaintext []byte) string {
	sum := blake3.Sum256(plaintext)
	return hex.EncodeToString(sum[:])
}

// ad is the content-scoped associated data (ADR-0001 §6): invariant across
// every reference to the chunk, so a deduped blob verifies for all of them.
// Positional binding lives in the manifest, not here.
//
// The \x00 separators are unambiguous only because every field is \x00-free
// and `hash` is fixed-width hex. If a future format feeds a raw-binary or
// variable-alphabet address here (the E2EE ciphertext-addressing switch),
// switch to length-prefixed fields — tracked with the E2EE format work.
func (c *Cipher) ad(hash string) []byte {
	ad := make([]byte, 0, len(adDomain)+1+len(c.tenant)+1+len(hash)+1)
	ad = append(ad, adDomain...)
	ad = append(ad, 0)
	ad = append(ad, c.tenant...)
	ad = append(ad, 0)
	ad = append(ad, hash...)
	ad = append(ad, 0, FormatVersion)
	return ad
}

// Seal encrypts a chunk. It returns the content hash (the storage key) and
// the blob: [FormatVersion][nonce][ciphertext+tag].
func (c *Cipher) Seal(plaintext []byte) (hash string, blob []byte, err error) {
	hash = ChunkHash(plaintext)
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, err
	}
	blob = make([]byte, 0, 1+len(nonce)+len(plaintext)+c.aead.Overhead())
	blob = append(blob, FormatVersion)
	blob = append(blob, nonce...)
	blob = c.aead.Seal(blob, nonce, plaintext, c.ad(hash))
	return hash, blob, nil
}

// Open decrypts a blob and verifies it is the chunk with the given hash.
// The AEAD tag (bound to the content-scoped AD) plus a plaintext-hash
// recheck ensure the blob is exactly the expected chunk.
func (c *Cipher) Open(hash string, blob []byte) ([]byte, error) {
	hdr := 1 + c.aead.NonceSize()
	if len(blob) < hdr+c.aead.Overhead() {
		return nil, fmt.Errorf("crypto: blob too short")
	}
	if blob[0] != FormatVersion {
		return nil, fmt.Errorf("crypto: unknown format version %#x", blob[0])
	}
	nonce := blob[1:hdr]
	pt, err := c.aead.Open(nil, nonce, blob[hdr:], c.ad(hash))
	if err != nil {
		return nil, fmt.Errorf("crypto: open: %w", err)
	}
	if ChunkHash(pt) != hash {
		return nil, fmt.Errorf("crypto: chunk hash mismatch")
	}
	return pt, nil
}
