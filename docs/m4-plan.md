# M4 plan — Kamara stub (chunked storage + storage API)

- **Size:** ≤ 6 weekends per phase. Proposed split **M4a** (engine + API)
  then **M4b** (file UI), the same shape M3 used.
- **Status:** draft — format parameters open in `Q&A.md` Round 7. Starts
  after M3 (done + hardened).
- **Design home:** `kamara/SPEC.md` (living). This plan is milestone
  execution and dies when M4 ships; the SPEC persists.
- **Lifecycle:** working document, superseded by the M4 ADRs, the SPEC,
  and the worklog.

## Goal

Kamara stores files behind a clean storage API and a pleasant browser
upload experience, deployed per tenant like every catalog app, wired
through all four cross-cutting conventions — and its chunk format is
**E2EE-ready** so device sync and end-to-end encryption are additive
later, not a rewrite. The acceptance test: **Ergonomos attaches a file to
a task by calling Kamara's storage API**, and a person uploads/downloads
files in the browser.

## Design source (vetted, not inherited)

An earlier Go encrypted-sync prototype was analyzed as a design reference.
It confirmed the build order (chunking/upload works standalone; sync/E2EE
defer cleanly) and contributed the format-future-proofing discipline now
in `kamara/SPEC.md` §6 (per-blob version byte, per-tenant format-config
feature flags, reserved E2EE columns, associated-data binding). We adopt
those *ideas*; we do not fork its code (single-user, client-held-key
E2EE, P2P/relay, SQLite — all far from Kamara's server-side, multi-tenant,
Postgres, OpenFGA shape).

## What M4 attaches first (the format decisions — M4a)

These bake into the on-object layout and the Postgres schema the moment
the first file is stored, so they are decided before any byte lands (the
same "decide-before-first-byte" discipline M3 used for the conventions).
All are in `Q&A.md` Round 7 / `kamara/SPEC.md` §9:

1. Chunk sizing (single- vs two-tier).
2. Content-hash over plaintext vs ciphertext (dedup vs E2EE — reserve the
   field either way).
3. Cross-version reuse + ref-counting now vs later.
4. Blob backend (filesystem first, S3 behind the interface).
5. At-rest key-management shape (server envelope).
6. The per-blob version byte + per-tenant format-config row + reserved
   E2EE columns + associated-data binding (the E2EE-ready hooks).

## The shared transactional storage helper (issue #15)

M4 is where the multi-store consistency fix gets built once, because
Kamara's object + chunk + audit + search writes are the same Postgres DB
and must be one transaction. Build a small `unit-of-work` helper (ports
accept an executor satisfied by `*sql.DB` and `*sql.Tx`); Ergonomos adopts
it too, closing the confirmed seams in #15. New ADRs: chunk/storage format,
and the transactional-storage convention.

## Definition of done (M4a)

- [ ] Chunk/storage engine: content-defined chunking, content-addressed
      blobs behind a streaming `BlobStore`, ref-counted chunks + GC (if
      R7 Q5 says now), the E2EE-ready format (version byte, format config,
      reserved columns, AD binding).
- [ ] Storage API v0 (OpenAPI-first): create object, chunked/resumable
      upload, streamed/ranged get, permission-filtered list, delete —
      OpenFGA-authorized.
- [ ] Wired through the four conventions (pii export/erase, authz owner
      tuples, audit events, search feed), inside the shared transaction.
- [ ] Deployed as a catalog app (NeedsDatabase + NeedsOpenFGA; the blob
      backend provisioned per tenant).
- [ ] **Ergonomos calls Kamara's API** to attach a file to a task
      (the integration acceptance test).
- [ ] godog specs green (spec-first); `kamara/` seeded (README, SPEC,
      legal, `adr/`).

## Definition of done (M4b)

- [ ] HTMX file UI: upload (chunked/resumable, progress), list, download,
      delete; a11y CI gate as with Ergonomos.
- [ ] Demo: browser upload + Ergonomos file attachment, end to end.

## Out of scope (deferred, not dropped)

Device-to-device sync, E2E encryption + federated encrypted replicas, any
peer-to-peer/relay model, desktop/mobile sync clients, content-extraction
search, per-tenant chunker-seed anti-fingerprinting. The format is
designed so these are additive.

## Session schedule (indicative — settled after Round 7)

| Phase | Session | Work |
|---|---|---|
| M4a | 1 | ADRs (chunk/storage format, transactional-storage) + `kamara/` scaffold; the shared unit-of-work helper, Ergonomos migrated onto it (#15) |
| M4a | 2 | Chunking engine + `BlobStore` (filesystem) + chunk/object/version schema (goose), unit-tested |
| M4a | 3 | Storage API v0 (OpenAPI-first) wired through the four conventions, godog spec-first |
| M4a | 4 | Catalog deployment (per-tenant blob backend) + Ergonomos integration (attach a file), live-verified |
| M4b | 5 | HTMX file UI (chunked upload, list, download, delete) + a11y |
| M4b | 6 | Buffer + writing: SPEC/README updates, worklog, demo |
