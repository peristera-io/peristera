# ADR-0015: Transactional storage (unit of work)

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** GitHub issue #15 (M3 deep review); `docs/m4-plan.md`.
  Ergonomos adopts it; Kamara (M4) uses it from the first write.

## Context

Every app mutation touches several stores: the entity row, the audit
event, and the search feed — all in the **same per-tenant Postgres
database** — plus the OpenFGA authorization tuple, which is a **separate
system**. The M3 deep review confirmed the lack of a shared transaction
left real seams, the worst being a delete that destroyed the row but lost
the audit event — a mutation left with no audit record, breaking the
every-mutation-is-audited guarantee ADR-0011 exists to provide. The key
observation: three of the four stores are one database, so a plain local
transaction — not an outbox — removes most of the exposure.

## Decision

1. **`lib/dbtx`: a small unit-of-work over `database/sql`.** Stores operate
   on an `Executor` interface (`ExecContext`/`QueryContext`/
   `QueryRowContext`) satisfied by both `*sql.DB` and `*sql.Tx`, so the
   same store code runs inside or outside a transaction. `InTx` runs a
   function in a transaction, committing on success and rolling back on
   error or panic.
2. **Same-database writes of a mutation run in one transaction** — the
   entity row, the audit event, and the search-index write are atomic:
   all land or none do. This eliminates the destroy-without-audit case and
   the row-without-search / audit-without-row seams.
3. **The OpenFGA tuple write stays outside the transaction** (it is a
   different system with no shared transaction), and — inheriting
   ADR-0010's convention — the tuple is written *after* the row commits and
   deleted *after* the row is gone. This is the *one* remaining seam: a
   committed mutation with a not-yet-written or already-deleted tuple.
   Harmless to reads (a row with no tuple is invisible; a tuple with no row
   is dropped by the id-filtered fetch). Note "harmless to reads" is not
   "nothing to do": a create whose tuple write fails permanently is an
   *incomplete* create (an invisible row) that must be retried or failed,
   per ADR-0010 — not silently accepted. The dangling-tuple sweeper is
   tracked separately (issue #20).
4. **Reads and the personal-data export/erase hooks use the database
   directly** (no transaction) — only mutations need atomicity in M4.

## Consequences

- Three consistency seams collapse to one, and the dangerous silent cases
  are gone.
- App store implementations take an `Executor`, not a concrete `*sql.DB`;
  a per-app "stores bundle" is built over a `*sql.DB` for reads and over a
  `*sql.Tx` inside `InTx`.
- Kamara's object+chunk+audit+search writes are transactional from day
  one; Ergonomos is migrated onto the same helper.

## Alternatives considered

- **Transactional outbox** — the heavier answer #15 first proposed;
  unnecessary because the same-DB stores share a transaction directly.
  Revisit only if a store moves to a different database.
- **Per-mutation compensation / sagas** — fragile hand-rolled rollback for
  what a local transaction does correctly. Rejected.
- **Do nothing (document the seams)** — rejected: the destroy-without-audit
  case is a real, silent integrity loss.
