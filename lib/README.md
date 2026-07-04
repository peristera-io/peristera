# Peristera libraries (`lib/`)

Shared Go libraries every Peristera app depends on — the cross-cutting
conventions live here so no app reinvents them (README §4, §8).

**License: MIT** (`LICENSE`), unlike the AGPL apps — libraries stay
maximally reusable (ADR-0005).

## Packages

- **`id`** — UUIDv7 object identifiers (ADR-0007): time-ordered,
  index-friendly, dependency-free.
- **`pii`** — the personal-data metadata contract (ADR-0009): the
  descriptor registry (also the Article 30 view), the canonical
  data-subject identifier, retention classes, per-subject pseudonyms for
  indirect referrers (audit), the export/erase hook interface apps
  implement, and an in-memory pseudonym store for tests/dev.
- **`audit`** — the audit-event convention (ADR-0011): a typed,
  append-only emit path; the actor is pseudonymized (via `pii`) before
  storage so append-only rows never carry a raw subject ID.
- **`search`** — the unified-search feed (ADR-0012): the write side that
  every app calls on mutation; the query surface is deferred (issue #13).

Planned (M3b): `oidcrp` + `session` (issue #2).

Rule: a package earns its place here only once a second app needs it, or
it encodes a convention an ADR mandates for all apps.
