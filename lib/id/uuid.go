// Package id generates the object identifiers ADR-0007 mandates: UUIDv7,
// time-ordered so it indexes well in Postgres and carries no semantic
// content. Dependency-free (RFC 9562) — it is small and central enough to
// own rather than pull a library for.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// V7 returns a new UUIDv7 in canonical 8-4-4-4-12 string form. The first
// 48 bits are the Unix-millisecond timestamp, so lexical order ≈ creation
// order; the remaining 74 bits are random (version and variant fixed per
// RFC 9562).
//
// Ordering is at millisecond granularity: IDs from the same millisecond
// cluster together (preserving Postgres index locality, ADR-0007's need)
// but their relative order is unspecified — do not use V7 as a strict
// creation-order tiebreak within a millisecond.
func V7() string {
	var u [16]byte
	ms := uint64(time.Now().UnixMilli())
	u[0], u[1], u[2] = byte(ms>>40), byte(ms>>32), byte(ms>>24)
	u[3], u[4], u[5] = byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(u[6:]); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // variant 10
	return format(u)
}

func format(u [16]byte) string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}
