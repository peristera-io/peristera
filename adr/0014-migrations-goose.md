# ADR-0014: Schema migrations with goose, expand/contract

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** Q&A Round 6 (R32); working agreement #5 (expand/contract
  from the first migration); `docs/m3-plan.md`.

## Context

Ergonomos (M3) is the first app with a database schema, so it is the first
to need migrations. The control plane performs upgrades *for* customers and
clones tenants into staging (README §4), so rollback-ability and
zero-downtime are product features, not optional hygiene (agreement #5).

## Decision

1. **goose** is the migration tool (Q&A R32) — boring, Go-native,
   migrations embed in the app binary via `embed.FS`, run as an
   application startup step against the app's own database (ADR-0013
   database-per-app). No separate migration image.
2. **Expand/contract from migration one** (agreement #5): every migration
   is backward-compatible with the currently-running code. A destructive
   change (drop/rename/narrow) never ships in the same release as the code
   that requires it — it is a later *contract* migration once no running
   code references the old shape. This makes every release rollback-able
   and is what the control plane's staged upgrades and staging-clone flow
   depend on.
3. **SQL migrations, versioned and sequential**, in each app's
   `migrations/` directory, owned by the app (not shared) since each app
   owns its database.
4. **Migrations run before the app serves** (startup gate): the pod is not
   ready until its schema is current. Concurrent replicas race on goose's
   advisory-lock/version table — safe — but M3 apps are single-replica.

## Consequences

- The Ergonomos binary carries its schema history; `goose up` on boot is
  the whole migration story for M3.
- Expand/contract discipline is a review checklist item from the first
  migration — cheap now, impossible to retrofit onto a destructive habit.
- The staging-clone upgrade flow (2027) inherits a rollback-able history
  for free.

## Alternatives considered

- **golang-migrate** — equally boring; goose chosen for embed-friendliness
  and Go-function migrations should we ever need them (Q&A R32).
- **atlas** (declarative) — powerful diffing and can *enforce*
  expand/contract, but a heavier, less-boring dependency than the stage
  needs; revisit if hand-written expand/contract proves error-prone.
- **ORM auto-migrate** — hides destructive changes and defeats
  expand/contract review; rejected.
