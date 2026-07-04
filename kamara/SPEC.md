# Kamara — design specification (living)

> **This is a living document, not a plan.** It states what Kamara *is*
> and what is still *open*. It evolves with the design; it never
> "completes." A later contributor (human or agent) should be able to read
> this and gain deep working context. Milestone tasks live in
> `docs/m4-plan.md`; decision rationale lives in `adr/`; this doc states
> the design and links to the ADRs for the *why*.
>
> **Status: draft — the M4a format decisions are being settled in
> `Q&A.md` Round 7.** Items marked *(open — R7 Qn)* are not yet decided;
> items marked *(decided)* are settled framing.

## 1. What Kamara is

- **A file store with an API.** The primary consumer is not a human but
  another app: Ergonomos attaches a file to a task by calling Kamara's
  storage API. A browser upload UI is the second client. *(decided)*
- **Per-tenant, server-side.** One Kamara per tenant namespace, its own
  Postgres database, its own blob backend, its own OpenFGA store — like
  every catalog app (ADR-0013). No client daemon, no device sync, no
  peer-to-peer in M4. *(decided)*
- **Object identity is a UUIDv7**, and URLs carry the id, never a path or
  a content hash (ADR-0007). A rename or move never changes an object's
  URL. *(decided)*

## 2. Architecture

```text
browser / other apps
        │  storage API (HTTP, OpenFGA-authorized)
        ▼
   Kamara service (Go, HTMX UI)
     ├── metadata  → Postgres (objects, versions, chunks, ref counts)
     ├── chunks    → BlobStore (content-addressed blobs)
     └── authz     → OpenFGA (owner tuples, permission-filtered lists)
```

Files are split into content-defined chunks; chunk *bytes* live in the
BlobStore, chunk *metadata* and object/version records live in Postgres.
The Postgres database is the catalogue (no separate index needed). *(decided)*

## 3. Object and chunk model

- **Objects → versions → chunks.** An object (UUIDv7) has ordered
  versions; each version is an ordered list of chunk references. *(decided)*
- **Chunking: content-defined (FastCDC).** Boundaries are content-defined
  (rolling hash) so that inserting bytes doesn't reshuffle every
  downstream chunk — the property that makes later delta-sync and
  cross-version reuse work.
  - Sizing: single-tier vs two-tier — *(open — R7 Q3)*.
- **Content addressing.** A chunk's storage key is a hash of its bytes;
  the hash *algorithm is named in the tenant format config* (not
  hard-coded), and whether the hash is over *plaintext* (enables at-rest
  dedup) or *ciphertext* (E2EE) is a config-flagged choice — *(open — R7
  Q4)*. The field exists from day one regardless.
- **Chunk blob format.** Every stored blob begins with a **format version
  byte**, so a later encryption/format change is unambiguous and additive,
  not a rewrite. *(decided — the mechanism; the at-rest body layout is
  M4a work)*
- **Cross-version reuse + ref-counting.** On edit, chunks whose content is
  unchanged are reused; a `ref_count` + GC reclaims orphaned chunks.
  Whether to build this in M4a or defer — *(open — R7 Q5)*.

## 4. Storage backend

- **`BlobStore` interface**, so the concrete backend is swappable:
  `Put/Get/Delete/Exists`, **streaming** (`io.Reader` / ranged reads) so
  large files and browser uploads don't buffer whole blobs in memory.
  *(decided — the interface shape)*
- Concrete backend: S3-compatible (Scaleway/MinIO) vs filesystem —
  *(open — R7 Q6; ADR backlog #5)*. M4a can ship a filesystem impl and add
  S3 behind the same interface.

## 5. Data model (Postgres)

Shape (columns firmed up in M4a):

- `objects` — id (UUIDv7), owner subject, name (display), timestamps.
- `versions` — id (UUIDv7), object_id, ordinal, size, created.
- `chunks` — content hash (storage key), size, `ref_count`,
  **reserved E2EE columns** (`nonce`, `wrapped_dek`, `origin_version_id`,
  `origin_chunk_index`) — nullable at-rest, populated when E2EE lands so
  it is additive, not a migration. *(decided — reserve them now)*
- `version_chunks` — version_id, index, chunk hash.
- Plus the per-tenant **format config** row (§6).

Authorization is **not** in these tables — it lives in OpenFGA (ADR-0010).
The owner column is display/PII-scoping only, never the access decision.

## 6. Encryption stance

- **At-rest, not E2EE, in M4** — "encrypting everything is a hassle in the
  corporate world" (Q&A R6). Key management is a **server-side envelope**
  (per-tenant key), *not* a passphrase-derived client hierarchy. Details
  *(open — R7 Q7)*; ties to the per-tenant key hierarchy (ADR-0009 §6).
- **E2EE-ready format.** Three cheap hooks make E2EE additive later:
  1. the per-blob **format version byte** (§3);
  2. a per-tenant **format config** row — `{format_version,
     encryption_suite, hash_algorithm, chunker, features{read,write,check}}`
     — read first on open, so an at-rest tenant can later require an E2EE
     format and old clients refuse cleanly rather than corrupt;
  3. the **reserved E2EE columns** (§5).
- **Associated-data binding (adopt even at-rest).** Every stored chunk's
  AEAD carries fixed-length associated data binding it to
  `(tenant, object, version, chunk-index)`, so storage-layer access can't
  swap/reorder/replay chunks across objects. *(decided)*

## 7. Storage API v0

An HTTP API, OpenFGA-authorized, consumed by other apps and the browser:

- create object, chunked/resumable upload of a version, get (streamed,
  ranged), list (permission-filtered), delete. Exact surface is
  OpenAPI-first (ADR-0007), designed in M4a. *(decided — the approach)*
- The other-app integration (Ergonomos attaching a file) is the acceptance
  test for "storage API v0."

## 8. Cross-cutting conventions (files are user data)

Kamara wires the same four conventions as Ergonomos, via `lib/`:

- **Personal-data metadata** (`lib/pii`) — a file object relates to its
  owner; per-subject export returns their files, erase removes them
  (source before derived, ADR-0009 §3).
- **Authorization** (`lib/authz`) — OpenFGA `owner` relation per object;
  `Check` on access, `ListObjects` for listings.
- **Audit** (`lib/audit`) — upload/replace/delete emit pseudonymized
  events.
- **Search** (`lib/search`) — filename + metadata feed the FTS index
  (content extraction is later).

Kamara is also where the **shared transactional storage helper**
(issue #15) is built, since object+chunk+audit+search span the same
Postgres DB and must be one transaction; Ergonomos adopts it too.

## 9. Open questions

Tracked here so they don't get lost; being settled in `Q&A.md` Round 7.

| # | Question | Where |
|---|----------|-------|
| 1 | Reuse scope: port FastCDC/chunker ideas vs write fresh | R7 Q1 |
| 2 | M4a/M4b split (engine+API, then UI) | R7 Q2 |
| 3 | Single- vs two-tier chunk sizing | R7 Q3 |
| 4 | Content hash over plaintext (dedup) vs ciphertext (E2EE) | R7 Q4 |
| 5 | Cross-version reuse + ref-counting in M4a or defer | R7 Q5 |
| 6 | Blob backend: filesystem first, S3 behind the interface | R7 Q6, ADR #5 |
| 7 | At-rest key management (server envelope) shape | R7 Q7, ADR-0009 §6 |

Longer-horizon (not M4): device-to-device sync, E2EE + federated
encrypted replicas, resumable-sync protocol, desktop/mobile clients,
content-extraction search, per-tenant chunker-seed anti-fingerprinting.

## 10. Explicitly out of scope for M4

Device sync, E2E encryption, any peer-to-peer/relay model, desktop/mobile
sync clients, content-based search extraction. The *format* is designed so
these are additive; the *implementation* waits.
