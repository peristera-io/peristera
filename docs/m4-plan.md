# M4 plan — Kamara stub (chunked storage + storage API)

- **Size:** ≤ 6 weekends per phase. Proposed split **M4a** (engine + API)
  then **M4b** (file UI), the same shape M3 used.
- **Status:** parameters settled (`Q&A.md` Round 7, 2026-07-04). Starts
  after M3 (done + hardened). Settled: write fresh (port chunker + format
  ideas), split M4a/M4b, single-tier chunking, plaintext content-address
  (algorithm named in config), reuse+ref-counting in M4a, filesystem blob
  backend first, per-tenant DEK envelope-encrypting chunks at-rest.
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

- [x] Chunk/storage engine: content-defined chunking, content-addressed
      blobs behind a streaming `BlobStore`, ref-counted chunks + GC, the
      E2EE-ready format (version byte, format config, reserved columns, AD
      binding).
- [x] Storage API v0 (OpenAPI-first): upload, streamed get,
      permission-filtered list, metadata, delete — OpenFGA-authorized.
      (Resumable/ranged transfer deferred to M4b with the browser UI.)
- [x] Wired through the four conventions (pii export/erase, authz owner
      tuples, audit events, search feed), inside the shared transaction.
- [x] Deployed as a catalog app (NeedsDatabase + NeedsOpenFGA; per-tenant
      blob PVC + per-tenant DEK Secret provisioned), live on k3d.
- [x] **Live authenticated round-trip through the deployed storage API**
      (upload→list→download→delete) — the storage-API-v0 acceptance test,
      green on k3d against tenant `kam` with a PAT from the tenant's own
      issuer. *(Revised from "Ergonomos calls Kamara's API"; see below /
      R41.)*
- [x] godog specs green (spec-first, `features/kamara_storage.feature`);
      `kamara/` seeded (README, SPEC, legal, `adr/`).

**Acceptance revised (Q&A R41).** The original M4a acceptance —
*Ergonomos calls Kamara's API to attach a file* — was found to force the
platform-wide **service-to-service auth** decision (how one app
authenticates to another). That decision defines *all* S2S interaction in
Peristera and must be designed deliberately, not settled to make one test
pass. So: M4a's acceptance is a live authenticated API round-trip (no
cross-app call, no S2S decision); the cross-app file-attach moves to M4b
via **option C** (browser uploads to Kamara with the user's own session;
Ergonomos stores only the file-id reference — each app authorizes its own
user, so no cross-app trust); and the S2S/zero-trust model gets its own
design milestone (see `docs/s2s-auth-milestone.md`) before M6.

## Definition of done (M4b / M4c)

**Reshaped and split — see `docs/m4b-plan.md`** (Q&A Round 9). M4b/M4c became
the browser file experience over a new **folder hierarchy** in the Peristera
design language (Tailwind pilot): M4b = hierarchy model + API + browser auth
+ minimal UI; M4c = polish (extractable uploader, progress bar, file-details
drawer, design tokens, a11y gate, demo). The **Ergonomos attach flow moved
to the S2S milestone** (`docs/s2s-auth-milestone.md`, #29) as its acceptance
test — it validates the platform service-to-service model rather than
front-running it.

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
