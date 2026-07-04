# ADR-0011: Audit events

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** README §4 "Audit events"; M0 deferral; README §6
  ADR-backlog row 9 (erasure hard cases, incl. audit log); Q&A Round 6
  (R29); `docs/m3-plan.md`. (Not to be confused with GH issue #9.)

## Context

Every mutation emits a typed, per-tenant, append-only audit event.
Enterprise ask #1, NIS2 evidence, MSP support tooling, and our own
debugging depend on it — and it is impossible to retrofit, because old
code paths never emit events. Ergonomos is the first emitter; the
convention is `lib/audit`, not Ergonomos-shaped.

## Decision

1. **Typed, append-only events.** Shape: `id` (UUIDv7, ADR-0007),
   `time`, `actor` (a **subject pseudonym token**, not the raw subject ID
   — ADR-0009 §7, so the row never needs rewriting to erase a person),
   `action` (a typed verb, e.g. `ergonomos.task.completed`), `object`
   (type + id + permalink), and optional structured detail. Actions are
   enumerated per app, not free-text.
2. **Emit on every mutation**, via `lib/audit`. A mutation that doesn't
   emit is a bug; the API is deliberately hard to forget (the write path
   and the event live together).
3. **Storage: append-only, per tenant.** For M3 the events live in
   Ergonomos's own database (append-only table, no update/delete grants).
   The **unified per-tenant audit store** (one log across all apps, for
   the support/Article-30 view) is decided when the second app arrives —
   `lib/audit`'s emit interface stays stable so that move is transparent.
   This follows R29 (implement what the single app exercises; keep the
   contract reusable).
4. **The audit log is itself personal data** (actor + referenced
   subjects) yet must stay tamper-evident, so erasure cannot delete or
   rewrite rows (README §6 ADR-backlog row 9). Mechanism (pinned now
   because it shapes the immutable schema): events store a **per-subject
   pseudonym token** from day one (ADR-0009 §7), never the raw subject ID.
   Subject erasure deletes that subject's row in the `subject_pseudonyms`
   mapping — the append-only events are untouched and stay tamper-evident,
   but are no longer linkable to a person. This needs **no migration** of
   the audit table precisely because the token, not the ID, was written
   from the start. It uses a per-subject secret, *not* the per-tenant key
   hierarchy (ADR-0009 §6), which would nuke the whole tenant's log.
   M3 writes the token + populates the mapping; the erase operation itself
   (delete mapping row) is trivial and may land now or with the erasure
   story.

**M3 scope (R29):** `lib/audit` emit path + the append-only table; every
Ergonomos task mutation emits. Unified store, pseudonymized-mapping
erasure, and the audit-viewer UI are deferred (design-ready).

## Consequences

- The webhook/outbound-event story (README §4 "API-first") comes almost
  free later — the audit stream is the event source.
- Append-only is enforced at the DB grant level, not by convention.
- Every app must define its action vocabulary — a small per-app cost that
  keeps the log queryable.
- Audit retention is *not* a `lib/pii` retention class (ADR-0009 §4): the
  log is never erased, only pseudonymized, so its NIS2-evidence retention
  is governed by "append-only + pseudonymize-don't-delete", not by the
  class taxonomy. Stated so the two retention models don't get conflated.

## Alternatives considered

- **Log lines / unstructured logging as audit** — not queryable, not
  tamper-evident, not per-tenant-isolated. Rejected.
- **Add audit later** — old code paths never emit; unretrofittable.
  Rejected (the reason it's front-loaded at M3).
- **Delete audit rows on erasure** — breaks tamper-evidence and retention.
  Pseudonymization instead (§4).
