# Architecture Decision Records

Ecosystem-level decisions live here; each project folder gets its own `adr/`
for project-local decisions. Write an ADR for any non-obvious decision
(working agreement #3 in the root README) — if a future session (human or
LLM) could reasonably ask "why is it like this?", the answer belongs in an
ADR, not in chat history.

## Rules

- Copy `0000-template.md`, take the next free number (`NNNN-kebab-title.md`).
- Keep it short — half a page is the norm. Context, decision, consequences,
  alternatives. Link the Q&A round or discussion it came from if one exists.
- ADRs are immutable once accepted: a change of mind is a **new** ADR that
  supersedes the old one (update the old one's status line only).
- Statuses: `proposed` → `accepted` → (`superseded by ADR-NNNN` | `deprecated`).

## Index

- [ADR-0001](0001-monorepo.md) — One monorepo
- [ADR-0002](0002-stack.md) — Language and framework stack
- [ADR-0003](0003-kubernetes-only.md) — Kubernetes as the only deployment contract
- [ADR-0004](0004-build-vs-buy.md) — Build by default; three named exceptions
- [ADR-0005](0005-cla-and-licensing.md) — Licensing model and relicensing CLA
- [ADR-0006](0006-zitadel-integration.md) — Zitadel integration: shared deployment, virtual instance per tenant
- [ADR-0007](0007-permalinks-and-api-versioning.md) — Object identity, permalinks, and API versioning
- [ADR-0008](0008-control-plane-architecture.md) — Control-plane architecture: Tenant CRD + controller
- [ADR-0009](0009-personal-data-metadata.md) — Personal-data metadata: the GDPR-by-design contract
- [ADR-0010](0010-openfga-model-conventions.md) — OpenFGA authorization model conventions
- [ADR-0011](0011-audit-events.md) — Audit events
- [ADR-0012](0012-search-feed.md) — Unified search feed (Postgres FTS)
- [ADR-0013](0013-catalog-contract.md) — App catalog contract (code, not data — for now)
- [ADR-0014](0014-migrations-goose.md) — Schema migrations with goose, expand/contract
- [ADR-0015](0015-transactional-storage.md) — Transactional storage (unit of work)
