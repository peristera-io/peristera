# Peristera libraries (`lib/`)

Shared Go libraries every Peristera app depends on — the cross-cutting
conventions live here so no app reinvents them (README §4, §8).

**License: MIT** (`LICENSE`), unlike the AGPL apps — libraries stay
maximally reusable (ADR-0005).

## Packages

- **`pii`** — the personal-data metadata contract (ADR-0009): the
  descriptor registry (also the Article 30 view), the canonical
  data-subject identifier, retention classes, per-subject pseudonyms for
  indirect referrers (audit), and the export/erase hook interface apps
  implement.

Planned (M3a session 3): `audit` (ADR-0011), `search` (ADR-0012).
Planned (M3b): `oidcrp` + `session` (issue #2).

Rule: a package earns its place here only once a second app needs it, or
it encodes a convention an ADR mandates for all apps.
