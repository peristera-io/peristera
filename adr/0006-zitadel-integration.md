# ADR-0006: Zitadel integration — shared deployment, virtual instance per tenant

- **Status:** accepted
- **Date:** 2026-07-03
- **Provenance:** M1 confirmatory spike (`docs/m1-plan.md`; Q&A Rounds 4–5;
  worklog 2026-07-02/03). ADR-0004 decided Zitadel *at all*; this ADR
  records *how*, confirmed with running evidence on k3d.

## Context

Every Peristera app needs login/OIDC per tenant; the control plane must
provision tenants programmatically; tenant isolation is
namespace-per-tenant (ADR-0003); federation later rides on per-tenant OIDC
issuers. Zitadel v3+ is Postgres-only (14–18) and AGPL — both compatible
(we deploy it unmodified from the catalog; patches, if ever, must be
published).

## Decision

1. **One shared Zitadel deployment** (currently v4, Helm chart 10.x) in the
   platform namespace `peristera-system`, backed by a CNPG Postgres in DSN
   mode — **one Zitadel *virtual instance* per tenant**, each on its own
   domain. Measured: the whole set (Zitadel + Login v2 + Postgres) idles at
   ~240–260 Mi *total*, flat as instances are added; instance
   create/serve/delete verified self-hosted via the System API.
2. **Domain per tenant is a day-one rule.** The tenant domain is the OIDC
   issuer and must never change — it is what makes break-out (and later
   federation) possible. Corollary: apps and control plane read the IAM
   endpoint from per-tenant config even while all tenants share one
   deployment.
3. **Break-out seam, designed not built:** a tenant can start on — or be
   migrated to — a dedicated Zitadel in its own namespace (legal
   requirements, scale; natural premium tier). Migration path is
   `zitadel mirror --instance` + re-pointing the tenant domain
   (paper-checked; caveat: after per-instance mirroring, never mix a
   whole-system mirror into the same target).
4. **Login v2** (Zitadel's self-hostable Next.js login) serves all
   instances from one deployment, selected by host. The Node runtime in the
   catalog is an accepted, named trade-off (Q&A R20). Branding: colors,
   logo, hide-loginname-suffix, and watermark-off are per-instance API
   settings; custom layout/CSS is **not** supported — building our own
   login on the Session API stays the documented escape hatch if the brand
   outgrows this.
5. **Programmatic access = one system user** (`admin-client`), cert-based
   RS256 JWT, declared in deployment config. Hard-won specifics:
   - Roles ride on the **`MemberType: System`** membership:
     `SYSTEM_OWNER` (System API) **plus** `IAM_OWNER`
     (admin/management APIs inside every instance). A separate
     `MemberType: IAM` entry does not work — it wants a per-instance
     AggregateID.
   - The JWT **audience is always the deployment's ExternalDomain
     issuer**, even when calling a tenant instance's APIs.
6. **Tenant-IAM provisioning sequence** (what the M2 controller
   implements): `CreateInstance` (name, tenant domain, first org, owner) →
   `AddTrustedDomain` for the login's domain (without it Login v2 500s on
   the new instance) → create project + **public PKCE OIDC app** with
   `idTokenUserinfoAssertion: true` (else name/email claims are empty) →
   hand the clientId to the tenant's app pods.
7. **App-side OIDC shape:** auth-code + PKCE public client (go-oidc +
   x/oauth2); reference implementation `iam/cmd/stub` with headless E2E in
   `iam/e2e/` — verified end to end against a tenant instance.
8. **Entra ID / LDAP import** (why Zitadel won ADR-0004), paper-checked:
   LDAP and Entra ID (OIDC/SAML) exist as IdP templates, plus a bulk human
   import API. Hands-on when the first real migration appears (MSP alpha).
   Per-instance **quota/limits APIs** exist and map onto control-plane
   quotas/billing later.

## Consequences

- Marginal tenant cost ≈ 0; the ten-tenant single-VM MSP story holds.
- Shared blast radius and shared version across tenants — mitigate with HA
  replicas (stateless) and, for the extreme cases, the break-out flag.
- Tenant identity data lives outside the per-tenant crypto-shredding
  boundary: off-boarding = instance deletion + bounded backup-retention
  window; this goes in the DPA. (Deletion note: a just-created instance
  404s on the System API for a few seconds — projections; retry.)
- The M2 control plane owns the provisioning sequence above; the spike's
  curl calls are its specification.

## Alternatives considered

- **Zitadel deployment per tenant namespace:** cleanest isolation, killed
  by footprint at small scale (512 MB floor × N tenants); survives as the
  break-out/premium path.
- **One instance, org per tenant:** no per-tenant issuer/domain (breaks
  federation and break-out), org-level export is lossy. Rejected.
- **Own login UI now:** work without validating the model (Q&A R20);
  revisit only if Login v2 branding proves too limiting.
