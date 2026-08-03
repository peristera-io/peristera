// blobverify proves a restored Kamara tenant is actually restorable (#77):
// given the restored database, the restored blob directory, and the escrowed
// DEK, it walks every object's latest version manifest, decrypts every
// referenced chunk (the AEAD tag + plaintext-hash recheck in crypto.Open is
// the integrity proof), and checks the reassembled size against the manifest.
// Exit 0 = every file decrypts and matches; anything else is a failed restore.
//
// This is the DR-rehearsal tool (docs/dr-runbook.md), not part of any served
// image — run it from the repo against a port-forwarded restored DB:
//
//	go run ./cmd/blobverify -dsn "$DSN" -blobs ./restored-blobs \
//	  -dek ./dek -tenant demo.peristera.app
//
// -tenant is the crypto tenant the chunks were sealed under: the tenant's
// OIDC issuer host (what kamara passes to crypto.New at startup).
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" database/sql driver

	"github.com/peristera-io/peristera/kamara/internal/blob"
	"github.com/peristera-io/peristera/kamara/internal/crypto"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("DATABASE_DSN"), "restored kamara database DSN")
	blobDir := flag.String("blobs", "", "restored blob directory (the chunk store root)")
	dekFile := flag.String("dek", "", "DEK file (raw 32 bytes or base64 — the escrowed Secret value)")
	tenant := flag.String("tenant", "", "crypto tenant (the tenant's issuer host, e.g. demo.peristera.app)")
	flag.Parse()
	if *dsn == "" || *blobDir == "" || *dekFile == "" || *tenant == "" {
		flag.Usage()
		os.Exit(2)
	}

	dek, err := os.ReadFile(*dekFile)
	if err != nil {
		log.Fatalf("dek: %v", err)
	}
	cipher, err := crypto.New(decodeKey(dek), *tenant)
	if err != nil {
		log.Fatalf("cipher: %v", err)
	}
	blobs, err := blob.NewFS(*blobDir)
	if err != nil {
		log.Fatalf("blobs: %v", err)
	}
	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	objects, failed := 0, 0
	rows, err := db.QueryContext(ctx, `
		SELECT o.id, o.name, v.id, v.size
		FROM objects o
		JOIN versions v ON v.object_id = o.id
		WHERE v.ordinal = (SELECT max(ordinal) FROM versions WHERE object_id = o.id)
		ORDER BY o.id`)
	if err != nil {
		log.Fatalf("query objects: %v", err)
	}
	defer rows.Close()
	type obj struct {
		id, name, versionID string
		size                int64
	}
	var objs []obj
	for rows.Next() {
		var o obj
		if err := rows.Scan(&o.id, &o.name, &o.versionID, &o.size); err != nil {
			log.Fatalf("scan: %v", err)
		}
		objs = append(objs, o)
	}
	if err := rows.Err(); err != nil {
		log.Fatalf("rows: %v", err)
	}

	for _, o := range objs {
		objects++
		if err := verifyVersion(ctx, db, blobs, cipher, o.versionID, o.size); err != nil {
			failed++
			fmt.Printf("FAIL  %s  %q: %v\n", o.id, o.name, err)
			continue
		}
		fmt.Printf("OK    %s  %q  (%d bytes)\n", o.id, o.name, o.size)
	}
	fmt.Printf("\n%d objects verified, %d failed\n", objects, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// verifyVersion decrypts every chunk of the version's manifest in order and
// checks the reassembled length against the manifest size. crypto.Open's
// AEAD tag + plaintext-hash recheck is the per-chunk integrity proof.
func verifyVersion(ctx context.Context, db *sql.DB, blobs blob.Store, cipher *crypto.Cipher, versionID string, wantSize int64) error {
	rows, err := db.QueryContext(ctx,
		`SELECT chunk_hash FROM version_chunks WHERE version_id = $1 ORDER BY idx`, versionID)
	if err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return err
		}
		rc, err := blobs.Get(ctx, hash)
		if err != nil {
			return fmt.Errorf("chunk %s: %w", hash, err)
		}
		sealed, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("chunk %s: read: %w", hash, err)
		}
		pt, err := cipher.Open(hash, sealed)
		if err != nil {
			return fmt.Errorf("chunk %s: %w", hash, err)
		}
		total += int64(len(pt))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if total != wantSize {
		return fmt.Errorf("reassembled %d bytes, manifest says %d", total, wantSize)
	}
	return nil
}

// decodeKey accepts raw key bytes or base64 (the controller stores the DEK
// Secret base64-encoded text) — the same acceptance as kamara's own loadDEK.
func decodeKey(b []byte) []byte {
	b = bytes.TrimSpace(b)
	if len(b) == crypto.KeySize {
		return b
	}
	dec, err := base64.StdEncoding.DecodeString(string(b))
	if err != nil {
		log.Fatalf("dek: not %d raw bytes and not valid base64: %v", crypto.KeySize, err)
	}
	return dec
}
