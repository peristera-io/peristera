# ADR-0008: Control-plane architecture — Tenant CRD + controller

- **Status:** accepted
- **Date:** 2026-07-03
- **Provenance:** Q&A Round 5 (R23–R27), `docs/m2-plan.md`.

## Context

The control plane is the tenant lifecycle manager and the MSP product
(README §4). M2 needs its architectural shape: how it drives Kubernetes,
where its state lives, and how it authenticates operators.

## Decision

1. **A `Tenant` CRD (group `peristera.io`, `v1alpha1`, cluster-scoped) and
   a controller-runtime reconcile loop** — not imperative client-go in
   HTTP handlers. Reconciliation is this product's core competency:
   upgrades, staging clones, and quotas are all "converge reality to spec"
   problems. **Review rider (R23): revisit after M6** whether the
   controller pulls its weight.
2. **Tenant CRs are the source of truth.** No control-plane database until
   billing/quotas need one (2027). The UI reads CRs and their status; what
   the cluster reports *is* the tenant list.
3. **Reconcile creates, per tenant:** namespace, CNPG Postgres cluster,
   Zitadel virtual instance + trusted domain + project + PKCE app (the
   exact sequence in ADR-0006 §6), and the app pods from a **hardcoded
   catalog** (a Go slice; it becomes data when a second app exists). A
   finalizer tears all of it down on delete — off-boarding is a first-class
   operation from the skeleton. (Scope note: "off-boarding" here is
   whole-namespace teardown of live data; **crypto-shredding of backups** is
   a later addition — the backup / off-boarding milestone, README §5, #9.)
4. **Spec/immutability:** `spec.slug` is immutable (validation-enforced,
   ADR-0007) and forms the tenant domain = issuer. Display name and future
   knobs live in spec; issuer, clientId, phase, and conditions in status.
5. **The control-plane service** (Go + HTMX) is a k8s-API client with its
   own ServiceAccount; operators log in via OIDC against the *default*
   Zitadel instance (MSP staff live there — tenant users live in their own
   instances). Auth from day one; the M1 stub is the pattern.
6. **Secrets flow through Kubernetes**, not the control plane: the
   Zitadel system-user key and per-tenant credentials are Secrets the
   controller reads/creates; the UI never sees them.

## Consequences

- The control plane is stateless and restartable; a lost pod loses
  nothing. Everything an MSP sees is `kubectl`-inspectable.
- k8s controllers are a named LLM thin spot (ADR-0002): a
  `guidelines/` entry grows alongside the implementation.
- CRD versioning discipline starts at `v1alpha1` — conversion is a later
  cost we accept.
- One controller per cluster for now; multi-cluster is explicitly out of
  scope until the platform phase.

## Alternatives considered

- **Imperative provisioning in HTTP handlers + DB of record:** faster
  first demo, but drift, retries, and partial failures all become our
  code; rebuilt on the operator model within a year. Rejected (R23).
- **GitOps engine (Flux/ArgoCD) as the reconciler:** heavyweight
  dependency, wrong grain for per-tenant lifecycle + API-driven UX;
  contradicts build-by-default (ADR-0004). Rejected.
- **Control-plane Postgres now:** state in two places from day one, for
  no M2 feature. Rejected (R24).
