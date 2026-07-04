# Kamara ADR-0001: Chunk and storage format

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** Q&A Round 7 (R34–R40); `kamara/SPEC.md`. First
  component-local ADR (the pattern the root `adr/README.md` describes;
  ecosystem-wide decisions still live in the root `adr/`).
- **Corrected at the M4a session-1 review (2026-07-04), before any code
  depended on the format:** §5–§7 — the associated-data binding is
  content-scoped (not per object/version/index, which is incompatible with
  dedup) and the at-rest nonce lives in the blob header (not a metadata
  column).

## Context

Kamara stores files as content-defined chunks. The chunk and storage
format bakes into the on-object layout and the Postgres schema the moment
the first file is stored, so it is decided before any byte lands. Near-term
crypto is at-rest (not E2EE, Q&A R6), but the format must make E2EE
*additive later*, not a rewrite.

## Decision

1. **Content-defined chunking, single-tier.** FastCDC-style rolling-hash
   boundaries (min 256 KB / avg 1 MB / max 4 MB) — one tier, no boundary
   cliff (R36). Inserting bytes reshapes only local chunks, which is what
   makes cross-version reuse and later delta-sync work.
2. **Plaintext content-addressing.** A chunk's storage key is a hash
   (BLAKE3) of its plaintext bytes, so identical content dedups across
   files (at-rest storage savings). The **hash algorithm is named in the
   per-tenant format config**, so a future E2EE tenant can switch to
   ciphertext-addressing without a format rewrite (R37).
3. **Per-blob format version byte.** Every stored blob starts with a
   version byte; a later format/encryption change is unambiguous and
   additive.
4. **Per-tenant format config** row: `{format_version, encryption_suite,
   hash_algorithm, chunker, features{read,write,check}}`, read first on
   open — an at-rest tenant can later require an E2EE format and old code
   refuses cleanly rather than corrupts.
5. **Reserved E2EE columns** on the chunk/manifest record —
   `wrapped_dek`, `origin_version_id`, `origin_chunk_index` — nullable
   at-rest, populated when E2EE lands (additive, no row migration). The
   at-rest **nonce is not a metadata column**: each stored blob carries its
   own nonce in its header (§7), so a deduped blob (shared by many
   references) has exactly one nonce.
6. **AEAD associated-data binding is content-scoped.** Each stored chunk's
   AEAD binds to `(tenant, chunk-hash, format-version)` — **invariant
   across every reference to that chunk**, so one deduped ciphertext
   verifies for all references (a per-`(object,version,index)` AD would
   force one ciphertext per reference and defeat dedup, §2/§8). This
   prevents a chunk from being decrypted *as a different chunk*. The
   **positional binding** — which chunk sits at which index of which object
   version — lives in the **manifest** (the `version_chunks` rows in
   Postgres, written inside the ADR-0015 transaction and protected by the
   database), which prevents reordering/substitution within a version.
   When E2EE lands, the manifest itself is encrypted, carrying the ordering
   under the tenant/object key.
7. **At-rest encryption via a per-tenant DEK**, each chunk encrypted
   **exactly once**: the content-addressed store checks whether a blob for
   the plaintext hash already exists; if so only its `ref_count`
   increments (no re-encryption), else the chunk is encrypted under the
   per-tenant DEK with a **fresh random nonce stored in the blob header**
   (`[version byte][nonce][ciphertext][tag]`) and written. This avoids
   deterministic/convergent encryption and its chosen-plaintext pitfalls.
   Dedup is by plaintext-hash existence, which inherently lets a writer
   learn whether a plaintext already exists in the tenant (the standard
   dedup side channel) — acceptable within a single at-rest trust domain;
   the E2EE era switches to ciphertext-addressing (reserved, §2), removing
   cross-user dedup and closing it. The per-tenant DEK is held as a
   Kubernetes Secret in the tenant namespace, seeding the per-tenant key
   hierarchy (root ADR-0009 §6) — whole-tenant crypto-shredding later is a
   key deletion. A cloud-KMS envelope is a Scaleway-era upgrade behind the
   same seam (R40).
8. **Cross-version reuse + ref-counting + GC.** On edit, unchanged chunks
   are reused; a `ref_count` reclaims orphans (R38).
9. **Object identity is a UUIDv7 in URLs** (root ADR-0007), decoupled from
   chunk content hashes — a rename/move never changes an object's URL.
10. **Streaming `BlobStore`** interface (`io.Reader`/ranged reads),
    filesystem impl first (per-tenant PersistentVolume), S3-compatible
    behind the same interface at M6 (R39; root ADR backlog #5).

## Consequences

- At-rest ships real encryption in M4 (not deferred). E2EE later is
  additive: **no schema migration** (the E2EE columns are reserved), and
  the two things that *do* change — the AEAD associated-data construction
  and the addressing switch (plaintext→ciphertext hash) — are versioned by
  the per-blob format byte and the format config, so old and new blobs
  coexist. (Not "no rewrite at all": the encrypt path and AD construction
  are versioned, not untouched.)
- The chunk store is content-addressed and deduped; object metadata,
  versions, and chunk refs live in Postgres (the catalogue — no separate
  index).
- Files are user data: the object model carries the four conventions
  (root ADR-0009/0010/0011/0012), and writes use the transactional-storage
  helper (root ADR-0015).

## Alternatives considered

- **Two-tier chunk sizing** — the prototype's approach; its own review
  flagged the boundary as a reuse-defeating cliff. Rejected (R36).
- **Ciphertext content-addressing now** — blocks dedup for no at-rest
  benefit; it's an E2EE-era switch, reserved via the config (R37).
- **Plaintext-at-rest (no chunk encryption) in M4** — simpler, but then
  "at-rest encryption" isn't actually delivered; the per-tenant DEK is
  cheap and seeds crypto-shredding. Rejected (R40).
- **Fixed-size chunking** — no delta-reuse under insertion. Rejected.
