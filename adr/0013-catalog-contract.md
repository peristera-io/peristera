# ADR-0013: App catalog contract (code, not data — for now)

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** Q&A R26 (catalog as a Go slice) and **R31** (keep the
  slice, grow the contract; catalog-as-data deferred — this ADR is where
  R31's rider "don't lose the decision" is recorded); `docs/m3-plan.md`.

## Context

M2 shipped the tenant app catalog as a hardcoded Go slice with one entry
(the stub), deploying Deployment + Service + Ingress per app (ADR-0008).
M3 adds a second, real app (Ergonomos) that needs a database and an
authorization store — so the catalog entry must describe more than an
image and a port. Q&A R26 said "the catalog becomes data when a second
app exists"; R31 revisited that.

## Decision

1. **The catalog stays a hardcoded Go slice** — it does *not* become a CRD
   or config file now. Two curated entries do not justify a data model,
   and no MSP curates catalogs yet (YAGNI, Principle 1). **This
   consciously walks back R26**; recorded here so the decision is not lost
   (R31 rider).
2. **The catalog *contract* grows** — a `CatalogApp` entry declares its
   infrastructure needs, which the control-plane reconciler satisfies:
   - `NeedsDatabase` → a **dedicated database** for the app inside the
     tenant's CNPG Postgres cluster (database-per-app, Q&A R30); its DSN
     is injected into the app pod.
   - `NeedsOpenFGA` → access to the tenant's **per-namespace OpenFGA**
     (ADR-0003/0010); its API endpoint (and the app's store) is injected.
   - The existing env contract (`OIDC_ISSUER`, `OIDC_CLIENT_ID`,
     `PUBLIC_URL`, `LISTEN_ADDR`) is extended with `DATABASE_DSN` and
     `OPENFGA_API_URL` when the respective needs are set.
3. **Provisioning is idempotent and create-only** for M3 (matching
   ADR-0008): the reconciler ensures the database and OpenFGA exist;
   de-provisioning a single app mid-tenant, and drift correction, are the
   2027 control-plane alpha.
4. **Catalog-as-data is deferred, with a named trigger:** when an MSP
   needs to curate per-tenant catalogs (enable/disable apps per customer),
   the slice becomes a data model (CRD or per-tenant config). Tracked so
   it resurfaces at MSP alpha, not silently.

## Consequences

- Adding Ergonomos is a slice entry + its migrations, not a schema change
  to the platform.
- Database-per-app gives each app a clean erasure/backup boundary (feeds
  ADR-0009 erasure) while keeping one Postgres operator per tenant.
- The reconciler grows real provisioning logic (CNPG database, OpenFGA
  Deployment) — the heaviest part of M3b's control-plane work.
- Off-boarding stays whole-namespace: deleting the tenant drops every
  app database and the OpenFGA with the namespace (ADR-0008 finalizer).

## Alternatives considered

- **Catalog as a CRD/config now (per R26)** — speculative generality
  before any curation need; rejected (R31).
- **Schema-per-app in one shared database** — weaker erasure/backup
  boundary than database-per-app; rejected (R30).
- **A CNPG cluster per app** — an operator and failover surface per app,
  against "one Postgres per tenant"; rejected (R30).
