# ADR-0021: Certificate issuance and custom-domain tenants

- **Status:** accepted
- **Date:** 2026-07-11
- **Provenance:** Q&A Round 14 (R90, R91), building on R77/R79/R80 (Round 13).
  Supersedes the M7-s2 HTTP-01 cert wiring and the M7-s4 BYO-apex issuer model.
  Folds #52 (first-provision cert race) and #56 (custom-domain automation);
  `docs/post-m7-plan.md` is the delivery plan. Amends the domain model of
  ADR-0006 (Zitadel issuer) and ADR-0020 (Scaleway deployment/TLS).

## Context

M7 s2 shipped **HTTP-01 per-host** certificate issuance (one cert per
`<app>.<tenant>` host), chosen to get the spike green. That diverged from R77's
decision (**DNS-01 + per-tenant wildcard**) and is the root of **#52**: HTTP-01
needs the public A record live *before* the ACME challenge, so first-provision
races external-dns, NXDOMAINs, and wedges in cert-manager backoff for ~15 min.
A self-heal reconciler (`healTenantCerts`) papers over it.

M7 s4 added **custom-domain (BYO-apex) tenants**, where `spec.domain` (e.g.
`peristera.lu`) is the tenant's OIDC **issuer** and is **immutable** because the
issuer is a permanent identity. The customer points `A`/`*.A` at our IP; s4's
per-host HTTP-01 wiring issues the certs. This works but (a) inherits the
HTTP-01 race, (b) cannot issue a **wildcard** for the custom domain (HTTP-01
can't), and (c) conflates the issuer with the vanity domain, so a tenant can
never start on the platform host and move to a custom domain reversibly.

R90/R91 resolve all three.

## Decision

### 1. One DNS-01 wildcard cert story, platform *and* custom domains

- **Platform domain.** Issue a **per-tenant wildcard** `*.<slug>.peristera.app`
  via cert-manager **Scaleway DNS-01** (R77 (A)). No HTTP reachability, no
  external-dns race, one cert per tenant instead of one per host. cert-manager
  gets Scaleway DNS-write credentials (external-dns already holds them). Retire
  `healTenantCerts` and the per-host HTTP-01 path.
- **Custom domains.** Give them the **same DNS-01 wildcard** via
  **`_acme-challenge` CNAME delegation**: the customer sets, once,
  `_acme-challenge.<their-domain>` as a CNAME into a zone we control
  (`<slug>.acme.peristera.app`). cert-manager solves DNS-01 by writing the
  challenge TXT into *our* Scaleway zone; Let's Encrypt follows the CNAME
  (`cnameStrategy: Follow`). We never need write access to the customer's zone,
  and the customer sets only two one-time records in their own DNS: the wildcard
  `A`/`CNAME` at their apex, and the `_acme-challenge` CNAME.

A HTTP-01 wildcard is impossible, and DNS-01 on a domain we don't control needs
either NS delegation (heavy) or this CNAME delegation (light) — so CNAME
delegation is the mechanism that makes a custom-domain wildcard real.

### 2. Decouple the OIDC issuer from the vanity domain

The OIDC **issuer is the permanent identity** and lives on the platform host —
`<slug>.<base-domain>` (e.g. `demo.peristera.app`), or a dedicated `auth.` host.
It is set once at provisioning (`status.issuer`) and never changes. The
**custom domain becomes a mutable, reversible app-routing attribute**: app hosts
are `<app>.<domain>` when a domain is set, else `<app>.<slug>.<base>`. The
Zitadel virtual instance's domain is the **issuer host**, not the custom domain,
so provisioning no longer trusts the customer apex as an instance domain.

CRD consequence: `spec.domain` **stops being immutable**. Attaching, detaching,
or swapping it is an Ingress/cert change with **no identity impact** — tokens,
sessions, and OIDC registrations are untouched. The slug host can never be fully
retired (it stays the auth/issuer host), but it can be dropped as a user-facing
app host.

Code shape: split today's overloaded `tenantDomain()` into

- `issuerHost(t)` — always `<slug>.<base>` (permanent); feeds `status.issuer`,
  the Zitadel instance domain, and the issuer ingress;
- `appDomain(t)` — the custom domain when set, else `<slug>.<base>`; feeds app
  hosts, their ingresses, and the wildcard cert.

### 3. Domain-ownership verification (operator-initiated, self-serve-shaped)

Attaching a custom domain is gated on a **TXT ownership challenge**. On first
sight of an unverified `spec.domain`, the reconciler writes a random token to
`status.domainVerification` and publishes the expected record
(`_peristera-verify.<domain>` → the token). The operator sets it; the reconciler
resolves it and, on match, sets a `DomainVerified` condition. **App ingresses
and the custom-domain wildcard cert are not created until `DomainVerified`.**
This is exercised operator-initiated today (R80 keeps provisioning
operator-only); exposing it self-serve is #53, which adds only the tenant-facing
UI plus abuse controls (rate-limiting, duplicate-claim rejection, dangling-DNS /
takeover guards) that matter once tenants claim domains unattended.

### 4. BYO certificate — later premium

A paid tier may skip cert-manager: the customer uploads a cert+key into a
per-tenant Secret referenced as the Ingress `tls.secretName`. No architectural
blocker; the only added work is expiry monitoring (renewal is on them). Not
built now.

## Migration (the live `peristera.lu` tenant)

`status.issuer` is immutable per tenant, and `peristera.lu` was provisioned
under the s4 model with `issuer = https://peristera.lu`. The decoupling applies
to **newly provisioned tenants**; existing tenants keep their issuer, so the
change is non-breaking. This leaves a documented two-model coexistence:

- *legacy (pre-decoupling):* `issuer == custom domain`, `spec.domain` immutable
  in practice (changing it would orphan the issuer);
- *new:* `issuer == <slug>.<base>` (permanent), `spec.domain` a mutable vanity
  host.

The `peristera.lu` tenant may be left as-is (it works) or re-provisioned once
under the new model during a maintenance window if a single consistent model is
wanted before self-serve. The CRD relaxes the immutability rule for everyone;
legacy tenants simply should not have their domain changed (their issuer still
derives from it). This is enforced by a reconciler **migration guard**: a tenant
whose `status.issuer` host differs from its slug `issuerHost` (i.e. the issuer
sits on a custom apex — the legacy shape) has its **app hosts pinned to that
issuer host**, so a later `spec.domain` edit is ignored for routing rather than
splitting the tenant across two domains. Surfacing that no-op to the operator (a
`DomainPinned` condition / event) is a follow-up.

Two known follow-ups on the now-mutable `spec.domain`, both fine while
provisioning stays operator-only (R80) and closed before self-serve (#53): the
tenant's **primary (stub) OIDC client** redirect URIs are frozen at first
provision (only the catalog apps' clients re-converge each reconcile), so a
domain *swap* must also re-assert them — folded into the slice-3 attach flow;
and there is **no collision/reservation check** yet (a `spec.domain` under the
platform base would shadow another tenant's default hosts), which the slice-3
ownership verification + a platform-zone reservation will cover.

## Consequences

- cert-manager gains a **Scaleway DNS-01 solver** (webhook) + a DNS-write
  credential; a ClusterIssuer (or per-tenant Issuer) configured for DNS-01 with
  `cnameStrategy: Follow`. This is infra config that must be **verified live**
  against the warm node (the R96 cloud-infra step).
- Per-tenant **wildcard** certs replace per-host certs → far less Let's Encrypt
  rate-limit pressure and no per-host sprawl. `healTenantCerts` is deleted.
- external-dns keeps **static `domainFilters`** for Peristera-owned zones only;
  it never manages a customer zone (customers own their DNS). #56's "dynamic
  external-dns zones" and `coredns-custom` sub-parts are dropped (R91).
- New CRD status fields (`domainVerification`, `DomainVerified` condition) and a
  relaxed `spec.domain` immutability rule (a versioned, additive CRD change).
- A domain-ownership verification state machine + a DNS TXT resolve in the
  reconcile loop.

## Delivery (staged, each code slice verified live where noted)

Beyond this ADR (the design of record):

1. **Issuer/vanity decouple + mutable `spec.domain`** — split `issuerHost` /
   `appDomain`, relax the CRD rule, add the legacy-domain guard. Pure Go + CRD +
   unit tests; no new infra.
2. **DNS-01 wildcard issuance** — Scaleway solver + ClusterIssuer, per-tenant
   wildcard Certificate, delete `healTenantCerts`. **Live-verify** issuance.
3. **Custom-domain CNAME delegation + ownership verification** — the
   `_acme-challenge` CNAME target, the TXT verification state machine, and
   gating custom-domain ingress/cert on `DomainVerified`. **Live-verify** the
   end-to-end attach flow.

## Alternatives considered

- **(B) Keep HTTP-01 per-host + make the self-heal robust** (R90 (B)) — ~1h, no
  new dependency, but keeps the race, the backoff, the per-host cert sprawl, the
  self-heal machinery, and cannot do wildcards. Rejected.
- **NS delegation for custom domains** — the customer delegates their zone (or a
  subdomain) to Scaleway; external-dns + cert-manager then fully automate,
  wildcard included. Cleanest automation but the biggest customer ask (hand over
  DNS control). Kept as a possible managed/premium option, not the default.
- **Issuer follows the custom domain** (the s4 model, generalized) — makes the
  cutover a one-way, non-reversible re-identification. Rejected: it defeats the
  reversibility R90 requires.
- **`_acme-challenge` served from our own resolver / DNAT tricks** — verified
  unnecessary (public resolution + hairpin already works; R91).

## Implementation notes

- **Slice 2 shipped per-host DNS-01, not wildcard consolidation (2026-07-12).**
  The DNS-01 switch alone fixes #52 (the actual problem); consolidating to
  wildcards is a cert-count optimization, deferred. A live finding drove this: a
  single Certificate carrying both the apex `<slug>.peristera.app` and
  `*.<slug>.peristera.app` **clobbers** — both SANs validate via the *same*
  `_acme-challenge.<slug>.peristera.app` TXT name with different values, and the
  Scaleway webhook keeps only one, so one SAN never propagates. Single-dnsName
  certs issue cleanly. The consolidation path (when taken) is therefore a
  **split**: one platform `*.peristera.app` covering all tenant *issuer* hosts +
  a per-tenant `*.<slug>.peristera.app` covering app hosts — distinct
  `_acme-challenge` names, no clobber (both verified to issue live). This also
  dissolves the cross-namespace-secret question, since issuer ingresses
  (platform ns) and app ingresses (tenant ns) each reference a wildcard secret
  in their own namespace.
- **Slice 3 shipped HTTP-01 for custom domains, not DNS-01-via-CNAME
  (2026-07-12).** Slice 2's DNS-01 switch would break renewal for custom-domain
  tenants (their zone isn't in Scaleway), so the control plane now selects the
  issuer per host: platform-base hosts → DNS-01 (`letsencrypt-prod`),
  custom-domain hosts → HTTP-01 (`letsencrypt-http01`). HTTP-01 is safe here
  because the customer's A record is already live before provisioning (no
  first-issue race). This is R90's original custom-domains-HTTP-01; the
  DNS-01-via-CNAME wildcard (Decision §1) is a later upgrade that needs the
  customer to set the `_acme-challenge.<domain>` CNAME. HTTP-01 also proves host
  control at issuance, so the `_peristera-verify` TXT ownership gate (§3) is now
  defense-in-depth for the self-serve era (#53) rather than load-bearing.
  Verified live: peristera.lu re-issued via HTTP-01; demo unchanged on DNS-01.
