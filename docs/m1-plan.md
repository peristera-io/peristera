# M1 plan — confirmatory Zitadel integration spike

- **Time box:** 2 weeks (≈5 nights-and-weekends sessions). Hard stop.
- **Status:** parameters settled (`Q&A.md` Round 4 + topology discussion,
  2026-07-02). Implementation in progress.
- **Lifecycle:** working document. After M1 it is superseded by
  `adr/0006-zitadel-integration.md` and the worklog entry; don't maintain it.

## Goal

Zitadel is decided, all-in (ADR-0004). M1 settles *how*, not *whether*: at
the end, Zitadel runs on a dev cluster, one test user logs in to a stub page
via OIDC, and the integration approach is written up as ADR-0006. The spike
is confirmatory — its job is to turn the settled priors below into evidence
(or disconfirm them, which is equally a success).

## Settled parameters (Q&A Round 4)

- **Dev cluster:** k3d (k3s in Docker) on the Mac — Docker is already
  installed. A 3-PC bare-metal k3s cluster follows later for real-world
  tests; nothing in M1 may assume single-node quirks.
- **Topology: one shared Zitadel deployment, one *virtual instance* per
  tenant.** The 512 MB floor is per *deployment*, not per virtual instance —
  virtual instances are logical (rows served by the same pods, selected by
  host header), so the marginal tenant is ~free. The shared deployment and
  its Postgres live in a platform namespace (`peristera-system`), like the
  control plane itself. Accepted trade-offs, recorded for ADR-0006: identity
  data sits outside the per-tenant crypto-shredding boundary (off-boarding =
  System API instance deletion + bounded backup-retention window), shared
  blast radius (mitigate: stateless HA replicas), shared Zitadel version
  across tenants.
- **Break-out seam (designed now, built later):** a tenant can be moved to —
  or provisioned from day one on — a dedicated Zitadel in its own namespace
  (resources, legal requirements; natural premium tier). Migration path:
  `zitadel mirror --instance` to the dedicated database, brief write freeze,
  re-point the tenant's domain. Three day-one rules keep this possible:
  1. **Domain per tenant, from the first tenant.** The issuer URL never
     changes; break-out and federation both depend on it.
  2. **Per-tenant IAM endpoint config** in apps and control plane, even
     while all tenants point at the shared deployment.
  3. **Break-out is also a provisioning-time flag**, not only a migration —
     the migration path only serves the "grew too big" case.
- **Login experience: Zitadel Login v2** (self-hostable Next.js app),
  branded. The Node runtime is swallowed reluctantly — building our own
  login UI before the overall model is validated is work without learning.
  Own-UI on the Session API stays the documented escape hatch.
- **Spike code is kept:** the stub relying party seeds `iam/` (README,
  legal files, first Go code); manifests and API calls are M2's raw
  material.
- **CloudNativePG from session 1** — Zitadel-on-CNPG is precisely the
  integration risk worth confirming.

## Background facts (checked 2026-07-02)

- Zitadel v3 is Postgres-only (14–18; CockroachDB dropped) and AGPL (we
  deploy unmodified from the catalog; patches, if ever, must be published).
- Virtual instances are created via the System API; self-hosted supports
  them.
- `zitadel mirror` migrates database-to-database and has an `--instance`
  flag; documented caveat: after per-instance mirroring, don't mix in a
  whole-system mirror to the same target.
- Official Helm chart: `zitadel/zitadel-charts`.
- Per-instance quota/limit APIs exist (built for Zitadel Cloud) — maps onto
  control-plane quotas/billing later; one line in ADR-0006.

## Questions the spike must answer (= ADR-0006 outline)

1. **Do virtual instances work as advertised, self-hosted?** Create and
   delete a second instance via the System API on k3d; wire its domain
   (locally: wildcard DNS, e.g. sslip.io); confirm one Login v2 deployment
   serves multiple instances by host. Measure the shared deployment's idle
   footprint (Zitadel + login + Postgres). Paper-check `mirror --instance`
   against the current release.
2. **Deployment method.** Does the official Helm chart work on k3s with an
   external CNPG-managed Postgres, and are its values sane to pin down as
   opinionated defaults? (The control plane will later template whatever
   this produces.)
3. **Login experience.** Deploy Login v2 and probe its branding limits per
   virtual instance: can it look like Peristera, not like Zitadel?
4. **Management/System API from Go.** Create an instance, org, and user
   programmatically — M2's control plane must provision tenants this way,
   so prove the seam now.
5. **OIDC integration shape for our apps.** Auth-code + PKCE from a Go +
   HTMX relying party against a *virtual instance's* issuer: session
   handling, logout, token refresh — the pattern every Peristera app copies.
6. **Entra ID / LDAP import** (a named reason Zitadel won ADR-0004):
   docs-level verification only. No hands-on — time box discipline.

## Definition of done

- [ ] Zitadel runs on k3d, backed by CloudNativePG Postgres, in
      `peristera-system`.
- [ ] A second virtual instance created (and one deleted) via the System
      API, reachable on its own domain, served by the shared Login v2.
- [ ] A test user of a virtual instance logs in to a Go + HTMX stub page in
      `iam/` via OIDC (auth-code + PKCE); the page shows their identity;
      logout works.
- [ ] Instance + org + user created from Go.
- [ ] Footprint numbers recorded (idle RAM/CPU: Zitadel, login app,
      Postgres — the whole shared set).
- [ ] Login v2 branding limits probed per instance.
- [ ] `adr/0006-zitadel-integration.md` accepted, answering questions 1–6
      and recording the break-out day-one rules.
- [ ] `iam/` exists with README + instantiated legal files (§7); worklog
      appended; README status block and §4 (IAM topology) updated.

Demoable artifact: a short screen recording of the login flow — M1 has no
public surface.

## Session schedule (indicative)

| Session | Work |
|---|---|
| 1 | k3d cluster up, CNPG operator, Postgres cluster, Zitadel + Login v2 via Helm chart running in `peristera-system` |
| 2 | Virtual instance #2 via System API, domain wiring, footprint numbers |
| 3 | `iam/` stub: Go + HTMX relying party, OIDC login/logout against the virtual instance |
| 4 | Instance/org/user provisioning from Go; Login v2 branding probe; Entra/LDAP + mirror paper checks |
| 5 | Write ADR-0006, worklog, README updates. Writing gets a full session — it is the actual deliverable and must not be squeezed out |

**Abort rule:** if by end of session 2 Zitadel does not run cleanly on
k3d + CNPG with a working second virtual instance, stop building and write
*that* up — a confirmatory spike that disconfirms is a success, not an
overrun. The time box ends with an ADR either way.

## Out of scope (deferred, not dropped)

- OpenFGA, audit events, personal-data metadata (attach before M3/M4).
- Control-plane provisioning of any of this (M2 — but the spike's manifests
  and API calls are its raw material).
- Building the break-out migration (designed, not built; the three day-one
  rules above are the M1 deliverable).
- User management UI, MFA/passkey policy tuning, SCIM, actual Entra/LDAP
  import runs.
- Federation, production/Scaleway deployment, performance budgets.
