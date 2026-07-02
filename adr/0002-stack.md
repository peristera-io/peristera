# ADR-0002: Language and framework stack

- **Status:** accepted
- **Date:** 2026-07-02
- **Provenance:** Q&A.md Rounds 1–3 (Q11–13, R15)

## Context

Solo founder, nights and weekends, development primarily LLM-assisted. The
stack must be boring, richly represented in LLM training data, and match the
Kubernetes-only platform (ADR-0003). One controlled environment is a product
principle: every additional language, database, or framework is a support
surface and an ADR-worthy exception.

## Decision

- **Backend: Go** — all services. Single binaries, the k8s ecosystem is Go,
  best-in-class LLM coverage.
- **Web UI: HTMX**, server-rendered from Go. **Svelte islands** only where
  the interaction model demands it (first known case: the Ergonomos block
  editor, which will also need a CRDT — library choice is a separate,
  deferred ADR).
- **On-device apps: Flutter** (mobile). Flutter Web is not used — browser
  surfaces stay HTMX/Svelte. The Kamara desktop sync client is an open
  decision (Go + native shell vs. Flutter desktop).
- **Database: PostgreSQL only**, one per tenant, via the CloudNativePG
  operator.
- **BDD: godog** — Gherkin `.feature` specs at domain/API level drive the
  specify → red → green → refactor loop.

Explicit non-goals: a second database, SPA-by-default frontends,
docker-compose as a supported target, building auth from scratch.

## Consequences

- Everything server-side lives in one language; hiring/handover story is
  simple; LLM sessions rarely leave well-trodden ground.
- Known thin spots in LLM coverage: CRDT internals, k8s
  operators/controllers — compensated by detailed ADRs and `guidelines/`.
- The Notion-like Ergonomos editor will not be HTMX; the Svelte+CRDT surface
  is planned, not improvised (README §4).

## Alternatives considered

- **SPA framework everywhere (React/Svelte-Kit):** more moving parts, splits
  logic across the wire, contradicts the server-rendered simplicity bet.
- **Other databases (per-purpose polyglot):** each addition is an operational
  and backup/GDPR surface; Postgres covers search (FTS), queues, and
  relational needs for years at this scale.
