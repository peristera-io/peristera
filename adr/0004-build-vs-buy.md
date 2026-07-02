# ADR-0004: Build by default; three named exceptions

- **Status:** accepted
- **Date:** 2026-07-02
- **Provenance:** Q&A.md Rounds 2–3 and the cold review (2026-07-02)

## Context

We want to own the stack top to bottom — that is what "controlled experience"
and the platform endgame require, and it is what makes the suite coherent.
But some components carry security-critical maturity (auth) or decade-scale
scope (an office editor) that a solo nights-and-weekends builder cannot
compress.

## Decision

**Default: build.** A third-party component earns its place only by fitting
*all* the constraints of this repository: runs in the per-tenant k8s catalog,
Postgres-backed, readable (Go strongly preferred), configurable down to
opinionated defaults, exportable and erasable (GDPR contract). Three
exceptions today:

| Component | Role | Rationale |
|---|---|---|
| **Zitadel** | bootstrap, **all-in** | Auth bugs are security incidents; Entra ID/LDAP import machinery took others a decade. No abstraction-layer hedge — if Zitadel fails us, fallbacks (Ory, Keycloak) are worse fits. Accepted as a named risk. |
| **OpenFGA** | **permanent** | Genuinely good, fits every constraint; ReBAC is exactly our domain. One instance per tenant namespace. |
| **OnlyOffice** | bootstrap | Document co-editing must exist at the first public demo (M5); an editor suite is a decade of work. Integrated behind a document-service interface, not absorbed. |

Everything else — federation protocol, sync engine, control plane,
collaboration engine — is built in-house. The federation protocol is
security-sensitive (identity assertions across trust boundaries); its design
ADR must include an explicit threat model, starting from the decided
v1 allowlist-only trust model.

## Consequences

- Coherent UX and full control of deployment, config, upgrades, and backups —
  third-party components are controlled through the control plane, not by
  owning their code.
- M1 is a *confirmatory* Zitadel integration spike: it settles *how*, not
  *whether*.
- Each exception is a dependency risk, tracked in README §10.

## Alternatives considered

- **Buy/wrap by default** (assemble Nextcloud-style from existing parts):
  faster start, but forfeits the opinionated, federated, uniform experience
  that is the entire differentiation.
- **Build auth in-house:** rejected — multi-year detour, every bug a security
  incident, conformance certification expected by MSPs (Q&A Round 2).
- **Abstraction layer over multiple IAM engines:** hedging cost with no
  credible second engine; all-in on Zitadel instead.
