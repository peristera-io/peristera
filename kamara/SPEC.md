# Kamara — design specification (living)

> **This is a living document, not a plan.** It states what Kamara *is*
> and what is still *open*. It evolves with the design; it never
> "completes." A later contributor (human or agent) should be able to read
> this and gain deep working context. Milestone tasks live in
> `docs/m4-plan.md`; decision rationale lives in `adr/`; this doc states
> the design and links to the ADRs for the *why*.
>
> **Status: M4 complete (2026-07-06).** M4a (engine + storage API +
> deployment), M4b (folder hierarchy + browser UI), M4c (uploader component,
> details drawer, design tokens, a11y, demo) all shipped and reviewed. The
> next storage work is the service-to-service auth milestone (#29) and the
> deferred follow-ups (#22–#41). This doc stays living — it states what
> Kamara *is* and what remains open (§9).

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

**Built (M4a session 2):** `internal/chunk` (gear-hash content-defined
chunker, single-tier, streaming), `internal/crypto` (BLAKE3 content
address + XChaCha20-Poly1305 encrypt-once with content-scoped AD),
`internal/blob` (streaming content-addressed filesystem store),
`internal/engine` (Ingest → chunk/dedup/seal/store → manifest; Reassemble
back), and the storage schema (`internal/store`, migration validated
against Postgres). All unit/round-trip/dedup/tamper tested. Next: the
object/version repositories on the ADR-0015 unit-of-work, the storage API,
and the four conventions.

## 3. Object and chunk model

- **Objects → versions → chunks.** An object (UUIDv7) has ordered
  versions; each version is an ordered list of chunk references. *(decided)*
- **Chunking: content-defined (FastCDC).** Boundaries are content-defined
  (rolling hash) so that inserting bytes doesn't reshuffle every
  downstream chunk — the property that makes later delta-sync and
  cross-version reuse work.
  - **Single-tier** sizing (min 256 KB / avg 1 MB / max 4 MB) — no
    two-tier boundary cliff. *(decided — R7 Q36)*
- **Content addressing.** A chunk's storage key is a hash of its **plain**
  bytes (so identical content dedups across files — at-rest storage
  savings). The hash *algorithm is named in the per-tenant format config*
  (not hard-coded), so a future E2EE tenant can switch to ciphertext-
  addressing without a format rewrite. *(decided — R7 Q37)*
- **Chunk blob format.** Every stored blob begins with a **format version
  byte**, so a later encryption/format change is unambiguous and additive,
  not a rewrite. *(decided)*
- **Cross-version reuse + ref-counting.** On edit, chunks whose content is
  unchanged are reused; a `ref_count` + GC reclaims orphaned chunks. Built
  in M4a. *(decided — R7 Q38)*

## 4. Storage backend

- **`BlobStore` interface**, so the concrete backend is swappable:
  `Put/Get/Delete/Exists`, **streaming** (`io.Reader` / ranged reads) so
  large files and browser uploads don't buffer whole blobs in memory.
  *(decided — the interface shape)*
- Concrete backend: **filesystem impl for M4a** (a per-tenant
  PersistentVolume); an S3-compatible impl (Scaleway/MinIO) behind the
  same interface arrives with the SaaS/Scaleway story (M6). *(decided —
  R7 Q39; ADR backlog #5)*

## 5. Data model (Postgres)

Shape (columns firmed up in M4a):

- `folders` — id (UUIDv7), owner subject, `parent_id` (nullable, NULL =
  root), name, timestamps. Folders are first-class objects with their own
  OpenFGA tuples (Kamara ADR-0002, M4b). *(decided — R9)*
- `objects` — id (UUIDv7), owner subject, name (display), `folder_id`
  (nullable, NULL = the owner's root — M4b), timestamps.
- `versions` — id (UUIDv7), object_id, ordinal, size, created.
- `chunks` — content hash (storage key), size, `ref_count`,
  **reserved E2EE columns** (`wrapped_dek`, `origin_version_id`,
  `origin_chunk_index`) — nullable at-rest, populated when E2EE lands so it
  is additive, not a migration. The at-rest **nonce lives in the blob
  header**, not here (Kamara ADR-0001 §5/§7). *(decided)*
- `version_chunks` — version_id, index, chunk hash. This is the
  **manifest**: it carries the positional binding (which chunk at which
  index of which version), written inside the ADR-0015 transaction.
- Plus the per-tenant **format config** row (§6).

Authorization is **not** in these tables — it lives in OpenFGA (ADR-0010).
The owner column is display/PII-scoping only, never the access decision.

**Hierarchy authorization (M4b).** Folders and files each have an OpenFGA
`owner` (user), a `parent` (folder), and a computed `can_access = owner OR
can_access from parent`. Create/move writes the `owner` tuple and, inside a
folder, a `parent` tuple; access checks and listings use `can_access`.
Per-owner trees today, so `can_access` reduces to `owner` — but the
inheritance edges are real, so folder sharing later is a viewer tuple, not a
schema change (Kamara ADR-0002; model accretion #19).

## 6. Encryption stance

- **At-rest, not E2EE, in M4** — "encrypting everything is a hassle in the
  corporate world" (Q&A R6). Chunk contents are envelope-encrypted
  server-side under a **per-tenant data-encryption key held as a
  Kubernetes Secret** in the tenant namespace — the seed of the per-tenant
  key hierarchy (ADR-0009 §6), so whole-tenant crypto-shredding later is a
  key deletion. A cloud-KMS envelope is a Scaleway-era upgrade behind the
  same seam. *(decided — R7 Q40)*
- **E2EE-ready format.** Three cheap hooks make E2EE additive later:
  1. the per-blob **format version byte** (§3);
  2. a per-tenant **format config** row — `{format_version,
     encryption_suite, hash_algorithm, chunker, features{read,write,check}}`
     — read first on open, so an at-rest tenant can later require an E2EE
     format and old clients refuse cleanly rather than corrupt;
  3. the **reserved E2EE columns** (§5).
- **Content-scoped associated-data binding (adopt even at-rest).** Each
  stored chunk's AEAD binds to `(tenant, chunk-hash, format-version)` —
  invariant across all references, so a deduped blob verifies for every
  reference and can't be decrypted as a *different* chunk. Positional
  binding (order within a version) lives in the manifest (§5), not the
  AEAD — a per-`(object,version,index)` AD would defeat dedup. *(decided;
  corrected at the session-1 review — Kamara ADR-0001 §6)*
- **Encrypt exactly once.** A chunk is encrypted on first store (random
  nonce in the blob header); later identical-content writes just increment
  `ref_count`. Dedup-by-plaintext-hash carries the usual "does this content
  exist here?" side channel — fine within a single at-rest trust domain,
  closed by ciphertext-addressing in the E2EE era (Kamara ADR-0001 §7).

## 7. Storage API v0

An HTTP API, OpenFGA-authorized, consumed by other apps and the browser.
Contract is `api/openapi.yaml` (OpenAPI-first, ADR-0007); handlers are a
thin adapter over the file domain (`internal/api`).

**Built (M4a session 3).** The v0 surface under `/v1`:

| Method | Path | Does |
|---|---|---|
| `POST` | `/files?name=` | Upload — raw body is the content; server chunks/dedups/encrypts; uploader owns it → 201 metadata |
| `GET` | `/files` | List the caller's files (permission-filtered via `ListObjects`) |
| `GET` | `/files/{id}` | File metadata |
| `GET` | `/files/{id}/content` | Stream the reassembled bytes |
| `DELETE` | `/files/{id}` | Delete + reclaim orphaned chunks |

- **Authentication (decided — session 3).** Every call carries a bearer
  token; it is validated against the tenant OIDC provider's **userinfo**
  endpoint (the same cheap validation the control plane uses), and the
  `sub` claim becomes the caller's subject `{instance: issuer-host, user:
  sub}`. So the *authenticated user* owns the file, never "the calling
  service." A short-TTL cache (lib/session, 60s) keeps every call off
  userinfo. **Tradeoff (accepted):** the cache TTL bounds how long a
  revoked/expired token is still honoured (≤ 60s) — decoupled from real
  token lifetime, inherited from the control-plane pattern. Acceptable for
  v0; tighten or introspect token `exp` if revocation latency ever matters
  more (tracked as an issue).
- **Upload is single-shot streaming in v0.** The body streams straight
  into the content-defined chunker; nothing buffers the whole file. The
  **resumable/ranged** protocol (session-based vs. content-range) stays
  open and lands with the browser UI (M4b) — the engine already chunks
  regardless of the wire framing, so it is additive.
- **Errors:** `ErrNotFound` → 404, `ErrForbidden` → 403, missing/invalid
  token → 401, missing `name` → 400.
- The other-app integration (Ergonomos attaching a file) is the acceptance
  test for "storage API v0," wired in **session 4**. The one **open
  inter-app question** it forces: how Ergonomos obtains a token to forward
  — forward the logged-in user's access token (owner = the user, simplest,
  the v0 assumption) vs. a service account with an OAuth2 actor/on-behalf
  token (owner = user, actor = Ergonomos, richer audit). Deferred to the
  session-4 wiring; `oidcrp` currently retains only the ID token, so
  either path needs the access token kept or a token exchange.

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

## 9. Decisions and what's still open

Settled in `Q&A.md` Round 7 (2026-07-04): write fresh on our stack porting
the chunker algorithm + format-future-proofing ideas (R34); split M4a/M4b
(R35); single-tier chunking (R36); plaintext content-addressing with the
algorithm named in the format config (R37); cross-version reuse +
ref-counting in M4a (R38); filesystem `BlobStore` first, S3 behind the
interface at M6 (R39); per-tenant DEK (k8s Secret) envelope-encrypting
chunks at-rest (R40).

Corrected at the M4a session-1 review (2026-07-04): the associated-data
binding is content-scoped (not object/version/index — incompatible with
dedup) and the at-rest nonce lives in the blob header, not a metadata
column (Kamara ADR-0001 §5–§7).

Settled at M4a session 3: the storage-API surface (`api/openapi.yaml`) and
its bearer/userinfo authentication (§7); the chunk-record and manifest
columns (`internal/store/migrations`).

**Still open (firm up during M4, record in `adr/`):** the resumable-upload
protocol shape (session-based vs. content-range), landing with the browser
UI (M4b); how Ergonomos obtains the token it forwards (§7, session 4); GC
trigger/cadence and the blob-orphan reconciliation sweep (a crashed upload
leaves a blob with no chunk row — invisible to a row-based sweep); how a
per-tenant DEK is generated and mounted (session 4 deployment).

**Longer-horizon (not M4):** device-to-device sync, E2EE + federated
encrypted replicas, resumable-sync protocol, desktop/mobile clients,
content-extraction search, per-tenant chunker-seed anti-fingerprinting.

## 10. Explicitly out of scope for M4

Device sync, E2E encryption, any peer-to-peer/relay model, desktop/mobile
sync clients, content-based search extraction. The *format* is designed so
these are additive; the *implementation* waits.
