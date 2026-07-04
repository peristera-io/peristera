# ADR-0012: Unified search feed (Postgres FTS)

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** README §4 "Unified search"; M0 deferral; README §6
  ADR-backlog row 10 (permission-filtered search); Q&A Round 6 (R29);
  `docs/m3-plan.md`.

## Context

Cross-app search is the single most-missed feature in M365, and it only
happens if it is a convention every app feeds from the start (README §4).
The write side is unretrofittable in spirit — an app that never fed the
index has nothing to find. Ergonomos is the first feeder.

## Decision

1. **Postgres full-text search**, no second search engine until it
   demonstrably breaks (README §4, Principle 5 "one controlled
   environment"). Documents are `tsvector`-indexed.
2. **Every app feeds the index on mutation** via `lib/search`: a
   `Feed(doc)` upsert carrying `id`/`type`/`permalink` (ADR-0007), the
   owning subject, and the searchable text; delete removes the entry.
   This write-side hook is the M3 deliverable.
3. **Results are permission-filtered through OpenFGA** at query time
   (`ListObjects`, ADR-0010) — search never leaks what the user can't see.
   **Completeness caveat (ADR-0010 §6):** `ListObjects` may be
   non-exhaustive and the FTS-hits ∩ visible-set intersection interacts
   badly with paging/ranking (you cannot page FTS then filter without
   under-filling pages). The query implementation (deferred, see §4) must
   treat this as a correctness concern, not just latency; recorded now so
   it isn't assumed away.
4. **Storage, like audit (ADR-0011):** M3 writes to Ergonomos's own
   database; the **per-tenant cross-app index** (the point of "unified")
   is decided with the second app, behind a stable `lib/search` interface.
5. **The search index is derived data** — rebuildable from source, and it
   carries personal data, so its `lib/pii` handling is "drop and let it
   rebuild" rather than a separate erasure path. Subject to the erasure
   ordering rule (ADR-0009 §3): source rows are erased *before* the index
   is dropped/rebuilt, so a rebuild cannot re-materialize an erased
   subject.

**M3 scope (R29):** the `lib/search` write-side hook + Ergonomos feeding
it on task mutations. The **query UI and cross-app aggregation are
deferred** — the ADR fixes the feed contract so later apps and the query
surface compose without retrofitting feeders.

## Consequences

- When the query UI lands, every app built since M3 already feeds it.
- Permission-filtering couples search latency to `ListObjects` cost (ADR
  backlog #10) — acknowledged, trivial at single-user scale.
- Derived-and-rebuildable keeps erasure/backup simple (no authoritative
  personal data lives only in the index).

## Alternatives considered

- **A dedicated search engine (Elastic/Meili/Typesense) now** — a second
  datastore, backup surface, and operational burden against Principle 5;
  Postgres FTS covers this scale for years. Rejected until it breaks.
- **Per-app search with no shared convention** — cross-app search never
  materializes (the M365 gap). Rejected.
- **Feed the index later** — feeders are unretrofittable in spirit; the
  write hook must exist from the first app. Rejected.
