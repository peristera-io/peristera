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

## 2026-07-03 — M2 session 5 (core): API + UI live, spec-first twice

- `api/openapi.yaml` authored before any handler; oapi-codegen stubs
  (`internal/server/gen`); handlers implement the generated interface.
  Slug is the path identifier (ADR-0007 §4 carve-out); every resource
  carries its permalink.
- **6 scenarios / 23 steps green**, including the new API feature:
  unauthenticated → 401 JSON; create → Ready → delete → 404 entirely
  through `/api/v1` with a **machine-operator PAT** (test provisions
  `operator-ci` via the system client — the same automation path an MSP
  script would use).
- Auth middleware guards UI (redirect to OIDC login) and API (bearer,
  validated via userinfo + 60s cache). The server registers its **own**
  OIDC app in the default instance at startup — same code path as tenant
  provisioning. One binary: the HTTP server is a manager Runnable.
- HTMX UI: tenant table with live phase polling, create form, delete
  with confirm; string catalog from the first template (EN content,
  FR/DE/LB are targets). **Browser demo:** operator logs in, creates
  `demo4` in the form, watches it flip to Ready with a clickable issuer
  (screenshots `/tmp/cp-ui-{pending,ready}.png`). `demo3` remains the
  standing tenant.
- Session-5 spill into session 6 (as planned): containerization,
  in-cluster deployment + RBAC, godog in CI.
- **Next: session 6 — Dockerfile + deploy manifests + CI job; then the
  M2 write-up (guidelines entry on controllers, README §5 update, demo
  recording).**

## 2026-07-04 — M3 deep review (4 adversarial agents) + hardening

- Ran a deeper, adversarial review of the M3 data path (4 fresh-context
  agents: consistency seams, lib correctness, authz/isolation,
  migrations/ops), each producing concrete failure sequences + CONFIRMED
  vs PLAUSIBLE verdicts.
- **Core identity/authz design validated**: OIDC `sub` not forgeable (ID
  token fully verified), no cross-tenant token replay, every mutation
  OpenFGA-Check'd, List OpenFGA-only. No isolation/forgery flaw.
- **6 confirmed bugs fixed** (commit 2433e35, verified live on erg3):
  authz.ListObjects prefix guard (was panic/500); Ergonomos Delete emits
  audit **before** destroying (was a silent destructive delete with no
  audit record — the worst finding); SetDone ErrNoRows→404 (was a 403
  leaking the driver string); control-plane DSN via net/url (percent-
  encode the password); lib/oidcrp state↔cookie binding (login-CSRF);
  audit.Emit rejects a zero actor.
- Key insight: **#15 is smaller than framed** — 3 of the 4 stores share
  one Postgres DB, so the proper fix is one local transaction, not an
  outbox. Updated #15 accordingly; the tx wrapper is deferred to the
  shared storage helper (M4). Filed #18 (per-tenant NetworkPolicy +
  OpenFGA authn, MSP-alpha), #19 (OpenFGA model version accumulation);
  noted the missing owner index on #13.

## 2026-07-04 — M3 complete: Ergonomos live, all conventions verified (sessions 5–6)

- **lib/oidcrp + lib/session** extracted (issue #2/#3 closed): the shared
  auth-code+PKCE+session flow, TTL-evicting store, cookie hardening
  (SameSite=Lax + Secure flag), end_session logout. Stub and control plane
  refactored onto it; 7-scenario e2e still green. Dockerfiles moved to
  repo-root build context (sibling lib/ dependency).
- **lib/authz** — the fourth convention lib (OpenFGA HTTP client). All four
  convention libs now exist; pii refactored to an app-owned Registry.
- **Ergonomos** (new module): task domain wired through all four
  conventions (unit-tested with fakes), Postgres stores, goose migrations,
  HTMX UI; deployed as a catalog app (NeedsDatabase+NeedsOpenFGA).
- **Live end-to-end** on fresh tenants (erg2/erg3): the session-4
  provisioning ran for real (per-app CNPG database + per-tenant OpenFGA),
  operator logged into Ergonomos, task creation fired **all four
  conventions** — verified in the tenant DB: task rows (UUIDv7), audit
  events with a *pseudonymized* actor token, search FTS match, OpenFGA
  owner tuples with instance-namespaced subjects.
- Two live gaps found + fixed: per-app OIDC clients (apps were sharing the
  stub's), robust AlreadyExists matching (structured Zitadel error, #8
  closed).
- **Session-6 a11y CI gate**: axe-core (via Playwright) over the
  headlessly-rendered Ergonomos page at WCAG 2.1 AA — 19 checks, 0
  violations; the M0 a11y deferral, due at M3.
- Two review rounds (4 agents); fixes applied, issues #15/#16/#17 filed
  (multi-store consistency, Postgres store tests, per-app DB roles/DSN
  rotation), #8 closed.
- **M3 done.** ADRs 0009–0014, the MIT lib/ (id, pii, authz, audit,
  search, oidcrp, session), and Ergonomos. Kill-criterion clock: pre-M6,
  on plan. **Next: M4 — Kamara stub** (file storage).

## 2026-07-04 — M3a complete + M3b foundations (sessions 1–4)

Autonomous run: settle M3 params (Q&A R6), then session-by-session with a
fresh-context review checkpoint after each (findings triaged fix-now /
issue / note).

- **M3a session 1** (`dc311ac`): ADRs 0009–0012 — the GDPR-by-design spine
  (personal-data metadata, OpenFGA conventions, audit events, search
  feed). 2 reviewers → fixed pre-commit: per-*subject* pseudonym (not the
  per-tenant key), audit indirection-token-from-day-one (no immutable-table
  migration), pinned canonical subject format, dropped denormalized owner,
  erasure-ordering + ListObjects-completeness caveats. Deferrals → issues
  #12/#13.
- **M3a session 2** (`0c8109d`): `lib/pii` (first MIT lib). 2 reviewers →
  fixed: guarded classes map, hardened pseudonym allocation-race contract
  (unique-on-subject + re-lookup) with a `-race` test, ctx checks. Erasure-
  ordering enforcement → issue #14.
- **M3a session 3** (`75d7000`): `lib/id` (UUIDv7), `lib/audit`,
  `lib/search` + `pii.InMemoryPseudonymStore`. 1 reviewer → fixed:
  audit.Detail PII warning + Emit validation, search.Feed requires Owner
  (permission-filter safety) + Permalink, no subject in error strings, id
  same-ms ordering caveat. All race-tested. **M3a done.**
- **M3b session 4** (`5b7aaa7`): ADR-0013 (catalog stays code, grows a
  needs-contract; catalog-as-data deferred per R31) + ADR-0014 (goose,
  expand/contract). CatalogApp declares NeedsDatabase/NeedsOpenFGA;
  reconciler provisions database-per-app (CNPG Database + DSN secret) and
  per-tenant OpenFGA (migrate init + server). Builds green; no app
  declares the needs yet, so it is live-exercised in session 5.
- One in-scope sizing call (no Q&A needed, both session-1 reviewers
  concurred): moved the `lib/oidcrp` extraction M3a→M3b (retrofittable
  cleanup was diluting the conventions milestone; paid when Ergonomos
  opens the auth code).
- **Remaining M3b: session 5** (Ergonomos app — new module, `lib/oidcrp`
  extraction, goose migrations, tasks wired through all four conventions,
  live OpenFGA/DB verification, godog spec-first) **and session 6** (HTMX
  UI + a11y CI + demo). A focused application build; the ADRs fully
  specify it.

## 2026-07-04 — M2 session 6: in-cluster, one-command env, CI — M2 done

- Control plane containerized (distroless) and **running in-cluster**:
  ServiceAccount + least-privilege RBAC, Deployment mounting
  `admin-client-tls`, ingress at `cp.127.0.0.1.sslip.io` — full suite
  green against the in-cluster deployment (no local controller).
- Bugs the move surfaced, all fixed: Zitadel's wildcard ingress swallowed
  the `cp.` host (traefik router priority annotation); **`iam` and `cp`
  are now reserved slugs** (CEL, rejected by the API server); rolling
  updates deadlocked on leader election (the UI/API Runnable now
  implements `NeedLeaderElection() = false`); `EnsureWebApp` reconciles
  redirect URIs so one logical app serves several public URLs.
- Leader election on — a local dev run and the in-cluster controller can
  no longer double-reconcile.
- **`hack/dev-cluster.sh`**: zero → full environment (k3d, CNPG, Zitadel,
  in-cluster DNS, images, control plane), idempotent, keypair generated
  into gitignored `.dev-secrets/`. It is also the CI recipe — new **e2e
  job** runs the godog suite on a fresh k3d in every push/PR (first run
  verifies on push). Suggested follow-up once it proves stable: add it to
  the required status checks.
- `guidelines/kubernetes-controllers.md` written (the ADR-0002 thin-spot
  mitigation): reconcile shape, idempotency records, finalizer rules,
  leader election, the pkill-by-port gotcha.
- **M2 done** (README §5 + status updated; `docs/m2-plan.md` closed).
  Kill-criterion clock note: still pre-M6, on plan. **Next: M3 —
  Ergonomos stub, but first its attachment list: personal-data metadata
  (incl. retention/legal holds), OpenFGA model conventions, audit
  events, search feed — each an ADR before the first byte is stored.**

## 2026-07-04 — M2 close-out review (5 agents) + fixes

- Ran five fresh-context reviewers (strategy, security, code quality,
  docs, correctness bug-hunt). No architectural blockers; M2's design and
  ADR-to-code fidelity held up. One real defect found and **fixed**:
- **Delete path could orphan a tenant's Zitadel instance.** A System-API
  404 from projection lag (delete shortly after create) was treated as
  "already gone" and the finalizer removed — leaking identity data,
  against ADR-0006's off-boarding contract. Fix (`deleteInstance` in
  tenant_controller.go): a 404 is trusted only when the instance's OIDC
  discovery has also stopped answering, else requeue; plus a
  domain-adoption fallback when `status.InstanceID` was never persisted.
  New godog scenario "Off-boarding leaves no orphaned instance" locks the
  invariant (delete-before-Ready, then assert no instance by domain).
  **Suite now 7 scenarios / 26 steps, green in-cluster.**
- Also fixed: removed committed `iam/stub` binary (+gitignore); refreshed
  the README status block (was dated 07-02 with 07-04 content), §5 M2
  prose, and `iam/README.md` status label.
- All other findings filed as GitHub issues (security authz model, CSRF,
  devMode, initial-admin lifecycle, RBAC narrowing, `lib/` extraction at
  M3, memory eviction, tests, key-hierarchy milestone home, etc.).
  **Process note added (README §8 / working agreements): check the open
  issue list when planning new work — fold matching follow-ups into the
  milestone that next touches that area.**
- **M2 declared complete.**
