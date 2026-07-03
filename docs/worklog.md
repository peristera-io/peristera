# Worklog

Append an entry after meaningful work (working agreement #3). Newest at the
bottom. One entry = date, what happened, and pointers to artifacts.

---

## 2026-07-02 — M0: repo bootstrap

- Strategy README completed through three Q&A rounds (`Q&A.md`) plus an
  uncontexted cold review; all review findings addressed.
- `git init`, initial commit preserving the pre-M0 state.
- Legal files generalized from "ergonomos" to Peristera (repo-wide CLA);
  placeholder templates in `templates/legal/`.
- Bootstrap ADRs 0001–0005 (monorepo, stack, k8s-only, build-vs-buy,
  licensing/CLA).
- CI: markdownlint + link check; CLA Assistant bot.
- Published to github.com/peristera-io/peristera.
- **Next: M1 — confirmatory Zitadel integration spike (2-week time box).**

## 2026-07-02 — M1 planned; spike session 1: Zitadel runs on k3d

- M1 plan written (`docs/m1-plan.md`); parameters settled in Q&A Round 4 +
  topology discussion. Key decision: **one shared Zitadel deployment, one
  virtual instance per tenant**, break-out seam designed in (domain per
  tenant, per-tenant IAM endpoint config, break-out as provisioning flag).
- Session 1 done: k3d cluster (`peristera-dev`, host ports 9080/9443),
  CloudNativePG operator, `zitadel-db` CNPG cluster, Zitadel v4 + Login v2
  via Helm chart 10.0.4 in `peristera-system`. OIDC discovery and Login v2
  serve at `http://iam.127.0.0.1.sslip.io:9080`. Manifests + walkthrough in
  `iam/` (README, legal files instantiated).
- **First footprint numbers (idle, minutes after boot): Zitadel 80 Mi,
  Postgres 91 Mi, Login v2 91 Mi — ~262 Mi for the whole shared set.** The
  feared 512 MB-per-tenant scenario is off the table if virtual instances
  hold up; re-measure under load and after instance #2 (session 2).
- Gotchas recorded in `iam/README.md`: k3d kubeconfig says `0.0.0.0` (macOS
  won't dial it); host port 8080 was taken locally, hence 9080; DSN mode
  with CNPG credentials worked first try (`sslmode=require`).
- **Next: session 2 — second virtual instance via the System API, domain
  wiring, Login v2 multi-host check.**

## 2026-07-02 — M1 spike session 2: virtual instances confirmed

- System API user `admin-client` added via chart values (cert-JWT auth;
  gotcha: instance ops need role `SYSTEM_OWNER`, not the chart-example
  `IAM_OWNER`). Wildcard ingress `*.127.0.0.1.sslip.io` for tenant domains.
- **Virtual-instance lifecycle works self-hosted**: `tenant-demo` created
  via `POST /system/v1/instances/_create` with its own domain, first org,
  and owner user; serves its own OIDC issuer
  (`http://demo.127.0.0.1.sslip.io:9080`); the shared Login v2 serves it by
  host. A throwaway instance created and deleted (deletion 404s for a few
  seconds after creation — projection lag, retry).
- **Footprint flat with a second instance: ~242 Mi total** (Zitadel 72,
  Postgres 92, Login 78). Topology prior holds: marginal tenant ≈ free.
- Session 2 walkthrough appended to `iam/README.md`.
- **Next: session 3 — Go + HTMX stub relying party in `iam/`, OIDC
  auth-code + PKCE login against the tenant-demo instance.**

## 2026-07-03 — M1 spike session 3: tenant login works end to end

- M2 parameters settled in Q&A Round 5 (CRD + controller with post-M6
  review rider, CRs as source of truth, IAM in tenant creation, stub as
  first catalog app, admin OIDC from day one).
- First Go code: `iam/cmd/stub`, a relying party doing auth-code + PKCE
  (go-oidc + x/oauth2) with in-memory sessions. **Headless E2E (playwright)
  logs `demo-admin` in on the tenant instance and out again: "Logged in as
  Demo Admin (`admin@demo.example`)".** E2E script kept in `iam/e2e/`.
- Three provisioning gotchas found and documented in `iam/README.md`
  (→ ADR-0006): system-JWT audience is always the deployment's
  ExternalDomain issuer; `IAM_OWNER` must ride on the `MemberType: System`
  membership; new instances need the login domain as a **trusted domain**
  (`POST /v2beta/instances/{id}/trusted-domains`) or Login v2 500s. Plus:
  `idTokenUserinfoAssertion: true` or name/email claims come back empty.
- The control-plane tenant-IAM sequence is now known exactly: create
  instance → trust login domain → project + PKCE app → clientId to pods.
- **Next: session 4 — provisioning from Go (instance/org/user/app), Login
  v2 branding probe, Entra/LDAP + mirror paper checks. Then ADR-0006.**
