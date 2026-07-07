# ADR-0019: Control-plane operator authorization (OpenFGA + audience)

- **Status:** accepted
- **Date:** 2026-07-07
- **Provenance:** Q&A Round 12 (R71 = OpenFGA, R72 = audience check);
  `docs/pre-m7-hardening-plan.md`; issue #1 (M2 close-out review). Extends the
  authorization stance of ADR-0010 (OpenFGA) from tenant data to the operator
  boundary. Refs #7 (the same "before public" hardening).

## Context

`requireAuth` (`control-plane/internal/server/auth.go`) authorized any request
carrying a browser session cookie *or* a bearer token that the default
Zitadel instance's userinfo accepts. That only proves *authentication* — a
live token/login from the default instance. There is **no audience check and
no operator check**: any principal who can authenticate to the default
instance becomes a full control-plane operator able to create and delete every
tenant. Harmless at single-operator dev scale; unacceptable once M7 puts the
control plane on the public internet.

README §4 answers "who may do this?" with OpenFGA everywhere. Operators are no
exception (Q&A R71 chose OpenFGA over a static allowlist or a Zitadel role
claim), so operator authorization becomes an OpenFGA check like every tenant's
data authorization — one consistent model, and an operator-management surface
later is just tuple writes, not a rewrite.

## Decision

**Two gates on every control-plane request: the token must be *for* the
control plane (audience), and its subject must be an *operator* (OpenFGA).**

### 1. Audience (authentication is not enough)

- **Browser cookie (oidcrp):** already audience-correct — the relying party
  verifies the ID token's `aud` equals the control-plane client. No change
  beyond resolving the subject from the session.
- **Bearer token (API/automation):** validated by **introspection** against
  the default instance (`/oauth/v2/introspect`), which returns `active`, `sub`,
  and `aud`/`client_id`. Reject unless active **and** the audience includes the
  control-plane client. This closes the "a tenant-app token passes" gap that
  plain userinfo left open. (Introspection also gives a real `active`/`exp`, so
  the bearer path no longer depends on a validity cache alone.)

### 2. Operator authorization (OpenFGA)

- A **platform-level OpenFGA** runs beside the control plane (in
  `peristera-system`), separate from the per-tenant instances (which are tenant
  data). It is the control plane's own authorization store.
- **Model:** a singleton `platform:peristera` object with an `operator`
  relation of `user`. The check is
  `platform:peristera#operator@user:<subject>`.
- Both gates resolved, `requireAuth` calls `authz.Check(subject, "operator",
  "platform:peristera")`; deny (403 API / login redirect UI) if not an operator.
- **Bootstrap:** the initial operator set is seeded from configuration
  (`OPERATOR_SUBJECTS`, the founder's `sub`) — the control plane writes those
  `operator` tuples on startup so the first operator is never locked out. A
  later operator-management surface writes/removes tuples through the same
  model; the config seed remains the break-glass.

## Consequences

- The control plane gains a dependency: a platform OpenFGA (CNPG-backed,
  preshared-key auth, `NetworkPolicy`-fronted — the same shape as the
  per-tenant deployment, ADR-0016/0010). Provisioned by the platform manifests
  (`hack/dev-cluster.sh`), not the tenant reconciler.
- **Lock-out is real:** an empty `OPERATOR_SUBJECTS` on a fresh deployment
  means no one can operate. The seed is mandatory in production; document it.
- The bearer path now introspects (a call per distinct token, cached briefly)
  instead of userinfo — a small latency cost for a real audience decision.
- This is the operator half of the platform authz story; per-tenant data authz
  (ADR-0010) is unchanged. A future federation/MSP layer (multiple operator
  orgs, scoped operators) extends the model (e.g. `operator` on a `tenant`
  object) rather than replacing it.

## Alternatives rejected

- **Static subject allowlist** (config only): simplest, but a second authz
  mechanism divergent from the platform's OpenFGA answer; chosen against in
  R71. (It survives as the *bootstrap seed* into OpenFGA, not the check.)
- **Zitadel role claim** (`operator` role on the control-plane project):
  IdP-native, but puts authorization in the IdP rather than the one place
  README §4 designates, and complicates a future cross-org operator model.
- **Audience via local JWT verification** instead of introspection: Zitadel
  access-token format is app-configurable (opaque is possible), so
  introspection is the format-independent check; local JWT parse stays a
  possible fast-path optimization, not the contract.
