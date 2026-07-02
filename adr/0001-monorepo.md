# ADR-0001: One monorepo

- **Status:** accepted
- **Date:** 2026-07-02
- **Provenance:** Q&A.md Round 2, R8

## Context

Peristera is several components (control plane, IAM, Ergonomos, Kamara,
shared libraries) built by a solo founder, primarily with LLM assistance.
LLM sessions work best with one coherent context; separate repos multiply
setup, CI, and cross-cutting-convention drift. Separate repos would, however,
suit per-project communities and per-project legal identity later.

## Decision

One monorepo: `github.com/peristera-io/peristera`, one subfolder per project,
shared Go libraries under `lib/`. The repo-wide CLA at the root covers all
contributions (see ADR-0005). A component is split into its own repository
only when a real community forms around it; `templates/legal/` exists to make
that split cheap.

## Consequences

- One place to read, one CI, one set of conventions — best possible LLM
  context.
- Cross-cutting changes (conventions in `lib/`) land atomically.
- Repo grows large over years; per-project access control is impossible —
  acceptable, everything is public anyway.
- A future split must migrate issues and re-establish CLA coverage.

## Alternatives considered

- **Multi-repo umbrella:** better per-project communities, but heavy overhead
  for one person and fragmented context for LLM sessions. Lost on both.
- **Monorepo with private control plane:** rejected in README §7 — hiding the
  most important component kills Phase 0 credibility with self-hosters.
