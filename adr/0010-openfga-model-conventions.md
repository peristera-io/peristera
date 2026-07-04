# ADR-0010: OpenFGA authorization model conventions

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** README §4 "One permission model: OpenFGA"; ADR-0003/0004
  (OpenFGA per namespace, permanent); README §6 ADR-backlog row 10
  (OpenFGA modeling); Q&A Round 6 (R29); `docs/m3-plan.md`.

## Context

"Who can see this?" must stay answerable in one click, across apps,
forever — the SharePoint failure mode OpenFGA exists to prevent (README
§4). Every app contributes to one shared authorization model instead of
growing its own ACLs. Ergonomos is the first contributor; the conventions
must be reusable and federation-ready, not single-user-shaped.

## Decision

1. **One OpenFGA instance per tenant namespace**, backed by the tenant's
   Postgres (ADR-0003; its own database — Q&A R30, `docs/m3-plan.md`). The
   control plane provisions it as part of a tenant's stack (M3 grows the
   catalog contract to include "needs an OpenFGA store").
2. **One shared model; each app owns a type namespace.** Types are prefixed
   by app: `ergonomos/task`, later `kamara/file`. Apps ship their type
   definitions as a module; the model is the union.
3. **Subjects are instance-namespaced users** (`user:<home-instance>/<id>`)
   so relations can point at remote users when federation arrives
   (ADR-0006/0007). Never a bare local user ID.
4. **Access goes through OpenFGA, always, and OpenFGA is the *sole*
   source of authorization truth.** On create, write the relation tuple
   (e.g. `owner`); on access, `Check`; for listings and search,
   `ListObjects`. **No denormalized `owner_id` authorization column** — a
   second copy invites dual-write divergence (tuple written, column not,
   on partial failure) and a "which wins?" ambiguity. Apps may store an
   owner for *display* only, clearly not load-bearing for access. (The
   tuple write and the row insert still share one hazard — see
   Consequences.)
5. **`lib/authz`** wraps the OpenFGA client with these conventions (write
   tuple, check, list-objects), so apps don't re-implement them.
6. **`ListObjects` is a known watch-item** (README §6 ADR-backlog row 10),
   on two axes: *cost* (permission-filtered lists/search can be expensive
   at scale) and *completeness* — `ListObjects` can return a bounded/
   non-exhaustive set, so intersecting it with a search/FTS result must
   not assume it is the full visible set, or legitimately-visible items
   get dropped, not just protected. Both noted, not solved, while
   single-user makes them trivial; the completeness caveat is load-bearing
   for ADR-0012's permission-filtered search.

**M3 scope (R29):** the `owner` relation on `ergonomos/task`; `Check` on
every access; `ListObjects` for the task list. Multi-relation modeling
(editor/viewer, sharing, groups) is deferred to when a second user needs
it — the conventions above make it additive.

## Consequences

- Cross-app "what can this user see?" is one `ListObjects` per type,
  answerable forever.
- Even single-user M3 pays a small tax (write tuple + check) — accepted,
  because retrofitting authorization is the failure mode this prevents.
- **Tuple write and source row insert are two writes** (OpenFGA and
  Postgres, no shared transaction). Convention: write the tuple *after*
  the row commits and treat a create as complete only when both land — on
  partial failure the create is retried/failed, never left with a row and
  no tuple (which would be invisible) or a tuple and no row (harmless
  dangling tuple, swept later). No denormalized column means there is only
  this one seam, not three.
- Tenant export/erasure naturally includes permission tuples (they are
  personal data too) — the `lib/pii` descriptor (ADR-0009) covers the
  OpenFGA store.

## Alternatives considered

- **App-level ownership columns / per-app ACLs** — the SharePoint sprawl
  this exists to kill; no cross-app answer. Rejected.
- **Defer OpenFGA to multi-user (M-later)** — the write path (tuples on
  create) is unretrofittable; single-user must already emit them. Rejected.
- **One OpenFGA for all tenants** — breaks namespace isolation and
  per-tenant erasure. Rejected (ADR-0003).
