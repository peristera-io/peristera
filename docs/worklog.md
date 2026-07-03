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

## 2026-07-03 — M1 closed: ADR-0006 accepted

- Branding probe: label policy (colors, logo, hide-suffix, watermark-off)
  is per-instance API surface; Login v2 renders neutral/clean; custom
  layout/CSS not supported — own-UI on the Session API stays the escape
  hatch. Entra/LDAP + `mirror --instance` paper checks folded into the ADR.
- **ADR-0006 accepted** — shared deployment + virtual instance per tenant,
  domain-per-tenant day-one rule, break-out seam, system-user specifics,
  and the exact tenant-IAM provisioning sequence for the M2 controller.
- DoD accounting: "provisioning from Go" deliberately moved to M2 session 3
  (the controller is where that code lives); screenshots + kept E2E script
  stand in for the screen recording. Everything else met, well inside the
  2-week box.
- **M1 done. Next: M2 — control-plane skeleton (`docs/m2-plan.md`),
  starting with the conventions ADRs (permalinks/API versioning) and the
  `Tenant` CRD.**

## 2026-07-03 — M2 session 1: conventions ADRs, Go CI, control-plane seed

- **ADR-0007 accepted** — object identity (UUIDv7, URLs carry IDs, names
  display-only), instance-namespaced federated references, immutable
  tenant slugs, `/api/v1` path versioning (additive-only within a
  version; pre-M6 breaks logged).
- **ADR-0008 accepted** — Tenant CRD + controller-runtime, CRs as source
  of truth (no control-plane DB), reconcile creates the full tenant stack
  per ADR-0006 §6, finalizer-based off-boarding, OIDC operator auth,
  post-M6 review rider (R23).
- **Go CI attached**: fmt/vet/build/test over every `go.mod` in the repo.
- `control-plane/` seeded: README, legal files, `apis/v1alpha1` Tenant
  types with immutable-slug CEL validation and the repo's first unit test
  (slug = DNS label).
- **Next: session 2 — CRD manifest + controller reconcile loop
  (namespace + CNPG cluster + finalizer) on the k3d cluster.**

## 2026-07-03 — M2 session 2: Tenant controller reconciles on k3d

- controller-runtime reconcile loop live-tested end to end: `kubectl apply`
  a Tenant → namespace `tenant-demo2` + CNPG Postgres appear, phase
  Pending → Ready; `kubectl delete` → finalizer + owner-reference GC tear
  everything down. Slug immutability enforced by CEL at the API server
  ("slug is immutable" on patch — rename literally cannot happen).
- Deepcopy + CRD manifest generated with controller-gen (markers in
  `apis/v1alpha1`); dev loop documented in `control-plane/README.md`.
- Gotcha: controller-runtime's metrics server defaults to `:8080`
  (collides on this workstation) — disabled for out-of-cluster dev.
- **Next: session 3 — Zitadel virtual-instance provisioning in the
  reconcile loop (the ADR-0006 §6 sequence, ported from the spike's curl
  calls to Go), issuer + clientId into Tenant status, external cleanup in
  the finalizer.**

## 2026-07-03 — M2 session 3: IAM in reconcile, spec-first with godog

- **BDD loop adopted** (working agreement #2, deviation in sessions 1–2
  acknowledged): `features/tenant_lifecycle.feature` written first, run
  red (issuer/clientId steps failed as expected), then implemented to
  green. **3 scenarios / 13 steps pass against the live cluster**:
  provision → Ready with a serving OIDC issuer, slug immutability,
  off-boarding kills the Zitadel instance (discovery stops answering).
  Suite is `PERISTERA_E2E=1`-guarded; joins CI when containerized.
- `internal/zitadel`: system-API client (self-signed RS256 JWT, fixed
  audience per ADR-0006 §5) — instance create/search/delete, trusted
  domain, org search, idempotent project + PKCE app via search-by-name.
- Reconcile walks ADR-0006 §6 one durable step per pass
  (`status.instanceId` / `status.clientId` as idempotency records,
  adoption by domain search if status is lost); finalizer deletes the
  instance before GC takes the namespace. Conditions DatabaseReady /
  IAMProvisioned; phase Ready = both.
- New instances get a **machine** org owner (`org-admin`) — human users
  arrive through the product (session 4/5 login demo creates one).
- Private key joined the `admin-client-tls` Secret (`tls.key`);
  controller config via env (`SYSTEM_USER_KEY` switches IAM on).
- Gotcha: `pkill -f cmd/controller` misses the compiled `go run` child —
  the zombie pre-IAM controller kept reconciling and held :8080. Kill the
  listener by port when in doubt.
- **Next: session 4 — deploy the stub app pod per tenant from the
  hardcoded catalog (ingress at `stub.<slug>.<base>`), human user
  creation, login on a freshly provisioned tenant end to end.**

## 2026-07-03 — M2 session 4: the full tenant slice, one kubectl apply

- Spec-first again: `tenant_apps.feature` red → green. **4 scenarios /
  17 steps pass**: the catalog app answers on `stub.<slug>.<base>`, sends
  logins to the tenant's own issuer, and the namespace holds generated
  `initial-admin` credentials (the MSP handover artifact).
- **Catalog env contract defined** (every app pod gets `OIDC_ISSUER`,
  `OIDC_CLIENT_ID`, `PUBLIC_URL`, `LISTEN_ADDR`); stub updated to it,
  containerized (distroless), imported into k3d. Reconcile deploys
  Deployment + Service + Ingress per catalog entry (create-only for M2;
  drift/upgrades are the 2027 alpha). Initial admin via management-v1
  user import; password satisfies the default complexity policy.
- **Architecture gotcha found and fixed:** inside a pod,
  `*.127.0.0.1.sslip.io` resolves to the pod itself — tenant pods
  couldn't reach their issuer. Fix: CoreDNS override answering those
  names with Traefik's cluster IP (`iam/deploy/dev/coredns-sslip.yaml`,
  incl. empty-NOERROR AAAA) + Traefik service port 9080 remapped onto
  the web entrypoint, so the *same issuer URL* works from browser and
  pod. In real environments this is ordinary split-horizon DNS; worth a
  line in the k3s-installer design later.
- **Demo:** `kubectl apply` Tenant `demo3` → 30s later a real browser
  logs in at `stub.demo3.127.0.0.1.sslip.io:9080` as "Initial Admin"
  with the credentials from the secret. Screenshot `/tmp/demo3-logged-in.png`;
  `demo3` left running as the standing demo tenant.
- **Next: session 5 — OpenAPI-first `/api/v1`, HTMX UI with operator
  OIDC login, in-cluster deployment, godog suite into CI (detailed plan
  in `docs/m2-plan.md`).**
