// Package engine is Kamara's chunking pipeline: it composes the chunker,
// the content-addressed blob store, and the at-rest cipher. Ingest turns a
// byte stream into an ordered, deduplicated, encrypted chunk manifest;
// Reassemble streams it back. This is the storage core the API and the
// object/version metadata layer sit on top of (ADR-0001).
package engine

import (
	"bytes"
	"context"
	"io"

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/chunk"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
)

// ChunkRef is one entry of a version's manifest: the content hash (storage
// key) and the plaintext size. Order is significant.
type ChunkRef struct {
	Hash string
	Size int
}

// Ingest splits r into content-defined chunks, and for each chunk not
// already stored, seals it once and writes it content-addressed (dedup:
// an existing chunk is neither re-sealed nor re-written). It returns the
// ordered manifest and the total plaintext size.
func Ingest(ctx context.Context, r io.Reader, c *crypto.Cipher, store blob.Store) (refs []ChunkRef, total int64, err error) {
	err = chunk.Split(r, func(plain []byte) error {
		hash := crypto.ChunkHash(plain)
		has, err := store.Has(ctx, hash)
		if err != nil {
			return err
		}
		if !has {
			_, sealed, err := c.Seal(plain)
			if err != nil {
				return err
			}
			if err := store.Put(ctx, hash, bytes.NewReader(sealed)); err != nil {
				return err
			}
		}
		refs = append(refs, ChunkRef{Hash: hash, Size: len(plain)})
		total += int64(len(plain))
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return refs, total, nil
}

// Reassemble streams the plaintext of a manifest to w by fetching, opening
// (AEAD-verified against each chunk's hash), and concatenating chunks in
// order.
func Reassemble(ctx context.Context, refs []ChunkRef, c *crypto.Cipher, store blob.Store, w io.Writer) error {
	for _, ref := range refs {
		rc, err := store.Get(ctx, ref.Hash)
		if err != nil {
			return err
		}
		sealed, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		plain, err := c.Open(ref.Hash, sealed)
		if err != nil {
			return err
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
	}
	return nil
}
