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

## 2026-07-06 — M4 complete: Kamara, the per-tenant file store

Built over three phases, each session fresh-context-reviewed (six adversarial
reviews total; findings triaged inline or filed as #22–#41).

- **M4a — engine + API + deployment.** Content-defined chunking (FastCDC),
  BLAKE3 content-addressing, XChaCha20-Poly1305 at rest under a per-tenant
  DEK, ref-counted chunk GC; streaming filesystem `BlobStore`; a bearer
  **storage API v0** (OpenAPI-first) authenticated via the tenant issuer's
  userinfo endpoint (the `sub` owns the file). Deployed as a catalog app —
  the first stateful-beyond-Postgres app: a per-tenant blob **PVC** and a
  per-tenant **DEK Secret**, provisioned by the control plane (surfaced +
  fixed a PVC RBAC gap). New: root ADR-0015 (transactional storage,
  `lib/dbtx` + `lib/pgconv`, closing #15), Kamara ADR-0001 (chunk format).
  Acceptance: a live godog round-trip (upload→list→download→delete +
  cross-subject isolation) through the deployed API with a tenant PAT.

- **M4b — folder hierarchy + browser UI.** Folders as first-class objects
  with OpenFGA `can_access` inheritance (Kamara ADR-0002, model accretion
  #19); create/upload-into/rename/move (cycle-checked)/delete, empty-first
  folder delete; export/erase cover folders. A browser file UI: OIDC cookie
  login beside the bearer API, an HTMX file browser (breadcrumb navigation),
  and full file operations — all cookie-authed (CSRF via SameSite=Lax), the
  browser surface never linking to the machine API. **Tailwind is the
  design-language pilot** (isolated `@theme` tokens, built via the standalone
  CLI → `go:embed`; htmx vendored + embedded, no CDN). a11y gate (axe,
  WCAG 2.1 AA) across four UI states.

- **M4c — polish + demo.** A drag-drop uploader **component** with a progress
  bar (progressive enhancement, extractable toward the planned SDK) and a
  file-details **drawer** (metadata + a stubbed Versions section — the schema
  already supports history). An end-to-end **Playwright demo** (real login →
  browse → create folder → upload → drawer → download) is the acceptance
  artifact (`kamara/demo/`).

- **The load-bearing decision (Q&A R41).** Wiring "Ergonomos calls Kamara's
  API" would have forced the platform-wide **service-to-service auth** model.
  Rather than settle it to pass one test, it was deferred to its own design
  milestone (`docs/s2s-auth-milestone.md`, #29) — where the Ergonomos
  file-attach flow becomes the acceptance test that validates the chosen
  model. M4a's acceptance became a same-app API round-trip; the cross-app
  attach moves to that milestone. The tuple-seam reconciler (#33) is the
  flagged gate before folder *sharing* ships.

- **Deferred (issues #22–#41, folded per agreement #7):** the S2S/zero-trust
  milestone (#29, folds #18/#19), the OpenFGA tuple-seam reconciler (#33),
  per-tenant storage quota (#27), blob-orphan GC sweep (#23), CSP (#38),
  streaming-server DoS hardening (#40), multi-file upload (#41), and more.

- **M4 declared complete.**

## M5 — service-to-service auth / intra-tenant zero-trust (in progress)

Plan: `docs/m5-plan.md`; decisions `Q&A.md` Round 10; roadmap renumbered
(M5 = S2S, M6 = OnlyOffice, M7 = public demo — R49).

- **Session 0 (docs):** plan + Q&A R10; renumber propagated to living docs;
  the per-tenant key-hierarchy / crypto-shredding convention homed at the
  backup/off-boarding story and #9 closed. Commits `49e8504`, `3a155c1`.
- **Session 1 (Cilium + network-enforced service topology, ADR-0016):**
  - **Cilium replaces flannel** as the dev-cluster CNI (flannel can't enforce
    NetworkPolicy). `hack/dev-cluster.sh` creates the cluster with flannel,
    k3s's netpol controller, and metrics-server disabled, then installs
    Cilium via helm in **kube-proxy coexistence** mode (`kubeProxyReplacement
    =false`; the `cilium` CLI bakes the host-side API address into pods on
    k3d/macOS, so helm). Reproducible self-host recipe.
  - **`CatalogApp.Calls []string`** (ergonomos → kamara) is the single source
    of truth for the service graph (ADR-0013 amendment); the controller
    generates per-tenant `NetworkPolicy` from it (`netpol.go`): each app
    accepts ingress only from the ingress controller + declared callers,
    egress only to same-ns + DNS + the issuer path; OpenFGA only from same-ns
    apps. Enforces cross-tenant isolation — the network half of **#18**.
  - **Two regressions surfaced by the CNI switch, both root-caused:**
    metrics-server can't scrape the kubelet under Cilium/k3d → kept the
    `metrics.k8s.io` APIService down → stalled *all* namespace GC → broke
    tenant off-boarding (disabled metrics-server, **#42**); and CNPG's 30-min
    default `stopDelay` let tenant DB pods ride out the grace period during
    teardown (capped `stopDelay=30`/`smartShutdownTimeout=10`).
  - **Verified:** 5/5 live topology probes (stub→kamara denied, ergonomos→
    kamara allowed, both cross-tenant probes denied) + full godog e2e green
    (provisioning, OIDC, Kamara storage, off-boarding within timeout).
  - **Adversarial review** → ADR-0016 corrected (the network layer blocks
    direct cross-tenant dials but not a Host-header *bounce* off the shared
    ingress to another tenant's *public* endpoints — internet-equivalent,
    auth-gated; internal surfaces stay isolated). Filed **#43** (close the
    public-surface bounce via L7/FQDN or internal issuer routing) and **#44**
    (netpol hardening: tighten np-openfga, drift-reconcile the kill-switch,
    label-identity). Commits `d560ed3`, `4551335`.
  - **OpenFGA preshared-key auth (completes s1 DoD, commit `1579d6d`):**
    per-tenant `openfga-authn-key` Secret; OpenFGA runs with
    `OPENFGA_AUTHN_METHOD=preshared`; apps get it as `OPENFGA_API_TOKEN` and
    `lib/authz` sends it as a bearer (`WithToken`); np-openfga tightened to
    the NeedsOpenFGA apps only (#44 item 1). Verified: OpenFGA 401 without
    the key / 200 with it, `stub` network-blocked from OpenFGA, full godog
    green. Closes the other half of **#18**.
  - **s1 complete.** Next: **S2 — machine identity + RFC-8693 token
    exchange** (`lib/oidcrp` retains the user access token; per-app Zitadel
    service user + JWT key; `lib/svcauth`).
- **Session 2 (machine identity + RFC-8693 token exchange, ADR-0017):**
  - **S2a:** `lib/oidcrp` retains the user's access token server-side (for a
    downstream service to exchange). Commit `caf8fdd`.
  - **Spike → solved (the hard part):** de-risked Zitadel v4.15.3 token
    exchange live. Cost most of the session — the recipe is non-obvious: the
    exchange CLIENT must be an **OIDC app with the token-exchange grant +
    `private_key_jwt`** (not a machine user / API app — Zitadel's
    `no active client not found` is its misleading error for "client lacks
    the token-exchange grant"); the SUBJECT token must carry the **project
    audience** scope; `enableImpersonation` opens the delegation path. Chose
    `private_key_jwt` over BASIC (no shared secret at rest). Commits
    `bce924b`, `5fb01ac`, `8ad973a` (thanks to the v2-UserService pointer
    that broke the client-type assumption).
  - **Build (verified — `TestS2SExchangeLive` + godog green):** `lib/svcauth`
    (`Exchanger.OnBehalfOf`, `ProjectAudienceScope`); Zitadel client methods
    (`ProjectID`, `EnsureS2SClient`, `AddAppKey`, `EnableImpersonation`);
    reconciler provisions a per-app **S2S OIDC client + key** for apps with
    `Calls` (mounted at `/mnt/s2s`, `SVCAUTH_KEY_FILE`/`OIDC_PROJECT_ID`
    injected — ergonomos only), enables impersonation per tenant; ADR-0017.
    Commits `d79f30b`, `90a2a7e`. Verified in-cluster + full godog.
  - **s2 complete.** Next: **S3** — callee-side local JWT validation
    (`lib/svcauth` server half) + the audit on-behalf-of actor (ADR-0011);
    then **S4** — the real Ergonomos→Kamara on-behalf-of upload (acceptance).
- **Sessions 3–4 (callee validation + acceptance):**
  - **Callee-side findings (live-spiked):** the exchanged token is Zitadel's
    *opaque* format, so "local JWT validation" is impossible — but it
    resolves to the **user** (`sub`) at Kamara's existing userinfo auth, so
    the acceptance needs **no Kamara change**. Introspection (not local JWT)
    is the right callee validation anyway: it works on the opaque token,
    checks live revocation, and recovers the calling service.
  - **S4 acceptance (R57, verified `TestS2SExchangeLive`):** `ergonomos/
    internal/kamara` (a tiny on-behalf-of client — exchange the user's token,
    then POST Kamara `/v1/files`), Ergonomos requests the project-audience
    login scope + builds the `Exchanger` from its mounted key + a `POST
    /attach` handler; the control plane injects `KAMARA_URL`. Proven live:
    **on-behalf-of upload → the file is owned by the USER** (their own token
    lists it); Ergonomos boots healthy with the wiring; full godog green.
    Commit `9a3aa8b`.
  - **S3 callee primitive:** `lib/svcauth.Validator` introspects a token as
    the callee's own S2S client (private_key_jwt) → `{Active, Subject,
    ActorClient}`; verified live. The remaining Kamara **audit-actor** wiring
    (confidential client + introspection auth + azp→audit plumbing +
    client-id→name map) is a real change to a security-critical path — filed
    as **#46** rather than rushed. Commit `3d5c3af`.

- **M5 substantially complete.** The intra-tenant zero-trust
  service-to-service model works end to end: Cilium-enforced network topology
  with cross-tenant isolation (#18), OpenFGA preshared-key auth, per-app
  machine identity, RFC-8693 on-behalf-of token exchange, and the acceptance
  (a real Ergonomos→Kamara upload owned by the user). Deferred (issues): #45
  (S2S key hygiene/rotation), #46 (Kamara audit actor). Adversarial reviews
  ran after s1, s2, and s3–4.
  **Next: M6 — OnlyOffice (OnlyOffice ↔ Kamara is now an S2S consumer of this
  model), then M7 — public demo.**

## 2026-07-06 — pre-M6 cleanup pass

Between M5 and M6, a batch of small security/correctness fixes from the issue
triage (commit `1d26031`, verified: full godog + oidcrp guard unit test +
the #19 restart check):

- **#4 CSRF:** `oidcrp.SameOriginGuard` on the cookie-authed HTMX UIs
  (ergonomos, kamara, control-plane) — Sec-Fetch-Site + Origin, same-origin
  only; never on the bearer APIs. **#37** Secure cookie behind HTTPS
  (control-plane; the others already were). **#5** devMode only for an http
  (dev) issuer. **#19** OpenFGA model-write dedup (reuse the latest matching
  model — verified the count stays stable across a restart). **#36** Kamara
  folder-delete race → 409 not 500.
- Closed #4, #5, #19, #36, #37. Residual noted: control-plane `/api/v1`
  accepts the session cookie (CSRF there is tied to #1).
- **Next: plan M6 — OnlyOffice.**

## 2026-07-06 — M6 s0 + s1 (browser office editing: Collabora, opt-in)

Direction switched from OnlyOffice to **Collabora Online (CODE)** after a
comparison (ADR-0018): lighter (no bundled Postgres/RabbitMQ), MPL-2.0, WOPI
maps ~1:1 onto Kamara's OpenFGA-gated file ops, stronger per-doc isolation.
Deployed as an **opt-in, per-tenant** engine — never shared, so no tenant's
decrypted document content is processed alongside another's.

- **s0 — spike + ADR.** Verified CODE `26.04.2.1` on k3d (`hack/spike/`):
  ~512 MB image, ~460–480 MiB idle; **connections unlimited by default** (the
  20-conn/10-doc cap is opt-in "home mode" only — beats OnlyOffice CE's hard
  20); WOPI allow-list permits cluster-private ranges; coolwsd enforces a **WS
  Origin** check (matters for the iframe embed). Drove a headless cool
  WebSocket load: coolwsd called the stub WOPI host's **CheckFileInfo +
  GetFile** under Cilium and LibreOffice **opened the doc** (`load success`).
  Token transport is `Authorization: Bearer`; **Collabora publishes no
  proof-key**, so the per-session access_token is the whole security boundary
  (R69 proof-key leg moot). PutFile save-back deferred to s3's browser demo (a
  raw-WS view-init artefact, not architectural). ADR-0018 + amendments to
  ADR-0004 (engine = Collabora) and ADR-0013 (optional-per-tenant dimension).
  Commit `6a28f64`.
- **s1 — catalog opt-in + provisioning.** `Tenant.spec.apps` opt-in set;
  `CatalogApp` gains `Optional`/`External`; the office engine (Collabora) is a
  dedicated provisioning path (`ensureOffice`: jail caps, WOPI env pinned to
  the tenant's in-cluster Kamara + frame-ancestors, own ingress) — no OIDC/DB/
  OpenFGA/S2S. Verified in-cluster on tenant `kam`: office absent until opted
  in, then Deployment/Service/Ingress + `np-office` appear and Collabora
  serves through its ingress; `np-kamara` admits office (editor→WOPI-host
  edge); live topology probe — **office→kamara OPEN, office→openfga BLOCKED**.
  Unit tests for the opt-in invariants.
- **Known gap (create-only, #47):** disabling an app in `spec.apps`
  does not deprovision it, and a `np-kamara` created before the office catalog
  entry keeps a stale caller set until recreated. Both are the M2 create-only
  limitation (drift/teardown is the 2027 alpha). Workaround: delete the stale
  `np-kamara`; manual cleanup on disable.
- **Next: s2 — Kamara WOPI host** (CheckFileInfo/GetFile/PutFile, OpenFGA-gated
  per-session access token) + the version-write path (save-back = new version) +
  #28 (Content-Disposition/fileType on GetFile).
- **s2 — Kamara WOPI host + version-write + #28.** Kamara now hosts the WOPI
  endpoints the office engine drives: `GET /wopi/files/{id}` (CheckFileInfo),
  `GET .../contents` (GetFile), `POST .../contents` (PutFile) —
  `internal/api/wopi.go`, a machine surface mounted at `/wopi/`. Auth is a
  per-session opaque access token (`internal/wopi`): scoped to (file, user,
  permission, TTL), stored only as a SHA-256, presented as a Bearer, and
  **re-checked against OpenFGA on every call** (Collabora has no proof-key, so
  the token is the whole boundary — a revoked share stops working at once, not
  at TTL; the token is bound to one file). Save-back writes a **new version**
  (`file.Service.WriteVersion`: ingest → `InsertVersion(ordinal+1)` →
  `SetObjectSize`; owner unchanged, editing user audited; `X-WOPI-ItemVersion`
  echoed) and `ListVersions` backs the drawer. **#28** folded in:
  `objects.content_type` (migration 00004), inferred on upload, served on
  GetFile and both downloads with a correct type + RFC 6266
  `Content-Disposition`. Tests: wopi session boundary (expiry, revoked-access,
  cross-file, unknown token), WOPI HTTP host (httptest), and a real
  `WriteVersion` round-trip (upload→edit→reopen shows the edit, two versions).
  In-cluster smoke: migration at v4, `wopi_sessions` + `content_type` present,
  `/wopi/` 401s without/with a bad token, kamara healthy.
- **Next: s3 — editor UI + acceptance.** The `/edit/{id}` page (cookie-authed)
  mints a session token and embeds Collabora; browser demo of open→edit→save→
  reopen-shows-change; the real Collabora↔Kamara round-trip in-cluster.
- **s3 — editor page + full round-trip acceptance.** The control plane injects
  `OFFICE_URL` + `WOPI_SRC_BASE` into Kamara when office is enabled;
  `wopi.Discovery` resolves Collabora's editor `urlsrc` (fetched via the
  engine's public URL through Traefik — Collabora bakes its public host into
  `urlsrc`, so no new netpol edge). `GET /edit/{id}` (cookie-authed) mints a
  per-session token and renders a WOPI auto-POST form embedding the Collabora
  iframe (token in the POST body, not the URL); WOPISrc is Kamara's in-cluster
  address so the engine fetches back intra-namespace. The details drawer is lit
  up (real version list + "Edit in office"). A browser e2e (`kamara/e2e`)
  drives it. **Acceptance verified in-cluster with all-real components:** real
  Collabora called CheckFileInfo + GetFile against the real Kamara host (real
  minted token, OpenFGA re-check, chunk decrypt, #28 content-type) and rendered
  the document; a real `PutFile` with that token wrote **version 1** (owner
  unchanged) and GetFile returned the edited bytes — the `versions` table shows
  ordinal 0 (upload) + 1 (saved edit). Automating a real edit inside
  Collabora's canvas proved unreliable (its editor UI, not our code), so the
  save leg was driven directly against the real host with the real token; the
  Collabora-emits-PutFile-on-save path is WOPI-standard and unit-tested in s2.
- **s4 — wrap.** Wired `Revoke`-on-delete (deleting a file drops its editing
  sessions, via a `SessionRevoker` hook so the domain doesn't import wopi;
  belt-and-suspenders over the per-call OpenFGA re-check). SPEC/plan/README
  updated. **M6 adversarially reviewed after s2** (no Critical/High; triaged
  the concurrent-save retry). Deferred as issues: **#47** (opt-in
  teardown/netpol drift), **#48** (office prod-hardening: caps, creds, TLS,
  token-in-ingress-logs).
- **M6 COMPLETE.** Browser office editing works end to end: a tenant opts the
  office engine in, a user opens a Kamara file in Collabora, edits, and saves —
  the edit lands back as a new version, owned by the user, with per-session
  OpenFGA-gated WOPI tokens as the trust boundary and no tenant's document
  content ever sharing an engine. **Next: M7 — public demo.**

## 2026-07-07 — pre-M7 hardening (triage Tier 1 + Tier 2)

Before M7 puts the control plane + tenants on the public internet, a security +
reliability batch from the issue triage (branch `pre-m7-hardening`; plan
`docs/pre-m7-hardening-plan.md`, Q&A Round 12). All verified in-cluster.

- **#30** blob-backed apps get the `Recreate` strategy + `replicas: 1` (no RWO
  PVC multi-attach on a rolling deploy).
- **#31** a tenant is `Ready` only once its app Deployments are Available
  (`Owns` Deployments + an `AppsReady` condition); a fresh tenant now goes
  Pending→Ready on the workloads, not on manifest creation.
- **#38** a Content-Security-Policy on the Kamara UI: `script-src 'self'
  'unsafe-eval'` with **no `unsafe-inline`** — the office-editor auto-submit
  moved to an external `/editor.js` and the drawer-close inline `onclick` to a
  delegated handler. Re-verified the editor e2e under the CSP.
- **#7** dropped cluster-wide secret `list/watch` from the control-plane SA:
  the manager no longer caches Secrets (get-by-name only), so RBAC is
  `[get, create]` — it can't enumerate the Zitadel masterkey / system key.
- **#6** the generated tenant-admin credential is `passwordChangeRequired` — a
  one-time handover, not a standing password.
- **#26** Kamara rejects an expired JWT locally (reads `exp`) before its token
  cache, closing the ≤TTL honour-window for JWTs (opaque tokens unchanged).
- **#1 (anchor, ADR-0019, R71=OpenFGA):** control-plane operator
  authorization. Two gates per request — audience (browser cookies via oidcrp;
  JWT bearer tokens audience-checked against the control-plane client; opaque
  PATs gated by the operator check) and an **operator** relation in a
  platform-level OpenFGA (`cp-openfga`, in-memory, preshared-key, in
  peristera-system). Operators seeded from `OPERATOR_SUBJECTS`;
  `hack/dev-cluster.sh` resolves the iam-admin sub. Verified: unseeded operator
  token → 403, seeded → 200, no/bad token → 401. godog now uses the seeded
  iam-admin PAT (also closes **#32**).
- **#48** office prod-hardening: **deferred** to M7's TLS work (R74 — office is
  not in the first public demo).

**Batch complete; a fresh-context review then merge to main. Next: M7.**

## 2026-07-07 — M7 s0 + s1 (cloud bootstrap, no meter yet)

Peristera onto the public internet (Scaleway/k3s, OpenTofu). Plan
`docs/m7-plan.md`, Q&A Round 13, ADR-0020.

- **s0 (merged):** a `images.yml` CI job builds amd64 and pushes
  `control-plane`/`kamara`/`ergonomos`/`stub` to `ghcr.io/peristera-io/<app>`
  (public). The catalog resolves our app images from `IMAGE_PREFIX`/`IMAGE_TAG`
  (`imageFor`), so dev stays `peristera-<app>:dev` (k3d-imported) and cloud
  pulls from ghcr. `TestImageFor`/`TestPublicURL`.
- **s1 http→https "scheme is config" (merged):** the reconciler's `publicURL`
  makes scheme/port config (`TENANT_SCHEME`, `TENANT_EXTERNAL_PORT`); tenant
  issuer + app URLs are `https://` on the cloud, `http://` in dev. (Also fixed
  the real godog PAT trailing-newline bug found greening CI.)
- **s1 cloud bootstrap (this branch, `m7-s1-cloud-bootstrap`):** the no-meter
  build — nothing applied yet.
  - **Tofu** (`deploy/scaleway/`): firewall (80/443 world; SSH + k3s API 6443 to
    `admin_cidr` only — Scaleway opens 6443 by default), Secret Manager entries
    for generated platform secrets (Zitadel masterkey, cp-openfga token,
    admin-client keypair — born in Tofu, never on disk/git), Object Storage
    state backend (`backend.tf.example`, two-phase), Cilium-ready cloud-init
    (flannel + network-policy disabled).
  - **bootstrap.sh** — cloud twin of `hack/dev-cluster.sh`: Cilium → cert-manager
    (LE HTTP-01) → external-dns (Scaleway) → ESO → CNPG → Zitadel → cp-openfga →
    control-plane → operator seed, all on real https.
  - **manifests/** — cloud twins with https + ghcr + Secret-Manager secrets:
    cert-manager issuer, external-dns values, ESO store+ExternalSecrets,
    Zitadel values (iam.<domain>, generated masterkey, per-host cert — no
    wildcard), CNPG, cp-openfga (ESO token), control-plane (ghcr image,
    `TENANT_SCHEME=https`).
  - **ADR-0020** — deployment architecture; **README** — operator runbook
    (deploy, remote state, pause/resume, teardown).
  - Validated offline: `tofu validate` green, `tofu fmt` clean, all rendered
    manifests parse, no leftover placeholders.
  - **Fresh-context adversarial review** (pre-apply, no meter). Fixed: the
    `scaleway-secret` root credential is now created in the external-dns
    namespace too (secretKeyRef is namespace-local — external-dns would have
    crash-looped, breaking all DNS→certs); external-dns `sources` narrowed to
    `[ingress]` (no record churn from the Traefik LB service); added a CoreDNS
    hairpin override (`coredns-custom`, twin of dev's `coredns-sslip`) so the
    control plane reaches Zitadel internally, not via node NAT-loopback; honest
    end-of-run status. Confirmed non-issues: the four-secret name/key chain
    matches end-to-end; the operator seed uses the chart's default iam-admin
    PAT exactly as dev does.

**Scope line:** s1 = platform (cp + iam) on TLS. Per-tenant TLS (each tenant's
Zitadel-issuer + app ingresses getting their own per-host certs) is **s2**
control-plane work, on the `TENANT_SCHEME=https` knob wired here.

**Applied + verified live (2026-07-07).** `tofu apply` (18 resources, node
`51.15.210.70`, firewall locked to the operator IP) → `bootstrap.sh` brought up
the whole stack. Two cloud-only bugs surfaced during apply and were fixed
(committed):

- **System-user key format.** The control plane parses the key with
  `x509.ParsePKCS8PrivateKey`; `tls_private_key.private_key_pem` is PKCS#1 for
  RSA → crashloop. Fixed by storing `private_key_pem_pkcs8` (dev's openssl key
  is PKCS#8, so dev never hit it). Same key material — the issued cert stays
  valid.
- **ACME challenge misrouted.** The control-plane Ingress carried dev's
  `router.priority: "1000"` (there to outrank Zitadel's `*.sslip.io` wildcard);
  on the public domain nothing competes for `cp.<domain>`, and the high
  priority captured `/.well-known/acme-challenge/*` before cert-manager's
  solver — so `iam` got a cert but `cp` didn't. Dropped the annotation.

Verified end to end: `https://cp.peristera.app` + `https://iam.peristera.app`
serve real Let's Encrypt certs (issuer `O=Let's Encrypt`); Zitadel OIDC
discovery 200; the seeded operator's PAT gets **200** from
`GET /api/v1/tenants` (proving control-plane→Zitadel hairpin over the CoreDNS
override + operator authZ), while no-token → 401 and non-operator → 403. The
CoreDNS hairpin override and ESO secret chain both worked first try. **M7 s1
complete.** Next: s2 — first operator provisions one tenant on real per-host
TLS.

## 2026-07-07 — M7 s2 (tenant TLS, reconciler code — offline, verify pending)

Root cause of the s1 tenant stall (found by creating a test tenant on the live
platform): `provisionIAM` blocks at `DiscoveryAlive(issuer)` — the control
plane polls `https://<slug>.peristera.app/.well-known/openid-configuration`,
which never answers because that host has no ingress/DNS/cert. (Dev doesn't hit
this: Zitadel's chart `*.<domain>` wildcard ingress covers tenant hosts, but
HTTP-01 can't issue a wildcard, so the cloud needs per-tenant ingresses.)

s2 wires it (branch `m7-s2-tenant-tls`, offline + unit-tested; **live verify is
the next, meter-spending step**):

- **Reconciler TLS config** (`TenantReconciler.TLSIssuer`, env
  `TENANT_TLS_ISSUER`; empty = dev/plain-http, `letsencrypt-prod` = cloud).
  New `internal/controller/ingress.go`: `ingressAnnotations`/`ingressTLS`
  helpers (cert-manager cluster-issuer annotation + per-host `tls` block, both
  no-ops in dev) and a **per-tenant issuer ingress** (`issuerIngress` builder +
  `ensureIssuerIngress`) — publishes `<slug>.<domain>` in the platform
  namespace, routing `/ui/v2/login`→`zitadel-login:3000` and `/`→`zitadel:8080`
  (mirrors the shared platform ingress), owned by the cluster-scoped Tenant so
  it's GC'd on off-boarding. Created early in `provisionIAM` so the cert can
  issue before `DiscoveryAlive` needs the host. App + office ingresses gain the
  same annotation + `tls` block.
- **CoreDNS override widened** to a regex over the whole `${DOMAIN}` zone
  (`rewrite name regex (.*)\.<domain> traefik… answer auto`) so every tenant
  issuer + app host resolves to internal Traefik (no NAT hairpin), replacing the
  two exact cp/iam rules. Control-plane cloud manifest gets
  `TENANT_TLS_ISSUER=letsencrypt-prod`.
- Dev unchanged (empty issuer → no annotations/TLS/issuer-ingress; the wildcard
  chart ingress still serves tenant hosts). `go build`/`vet` clean, controller
  tests green (`TestIngressTLSGating`, `TestIssuerIngress`).

**VERIFIED LIVE (2026-07-07, path a — branch image before merge).** Built the
s2 control-plane image via `images.yml workflow_dispatch` on the branch (added a
`type=ref,event=branch` tag rule so a feature branch pushes
`ghcr…/<app>:<branch>`), pointed the running control plane at it +
`TENANT_TLS_ISSUER=letsencrypt-prod`, applied the widened CoreDNS override, and
provisioned tenant `demo`. Result: **Ready** (DatabaseReady/IAMProvisioned/
AppsReady all True); `https://stub.demo.peristera.app` → 200 and
`https://kamara.demo.peristera.app` → 302 /auth/login, both on real per-host
Let's Encrypt certs; `https://demo.peristera.app/.well-known/openid-configuration`
→ 200. **The M7 acceptance is met.**

Wrinkle (not an s2 bug, filed): the FIRST cert (tenant issuer) hit a
DNS-vs-challenge race — cert-manager attempted HTTP-01 before external-dns
published `demo.peristera.app`; the challenge went `invalid` and cert-manager's
post-failure backoff is long, so it needed a manual reset (`kubectl delete
certificate tenant-demo-issuer-tls` → ingress-shim recreates it with no backoff
→ issued in ~45s). The three app certs issued first try (DNS warm by then). So
each brand-new tenant's issuer cert may lag on first provision until cert-manager
retries; runbook workaround = delete the stuck Certificate. Robustness follow-up
filed.

## 2026-07-07 — M7 s2 hardening: cert self-heal + metrics-server (verified live)

Two follow-ups surfaced provisioning tenants on the live cloud, both fixed and
verified end to end:

- **Cert self-heal (#52).** The external-dns/cert-manager first-issue race hit
  every new tenant's issuer + app certs, needing manual `kubectl delete
  certificate`. New `healTenantCerts` (control-plane, runs each reconcile,
  cloud-only): deletes any tenant Certificate that is `Ready=False` with
  `failedIssuanceAttempts >= 1` (i.e. in cert-manager's long post-failure
  backoff); ingress-shim recreates it fresh and, DNS now published, it issues.
  The failure gate means a still-issuing cert (0 failures) is never touched, so
  Let's Encrypt is never hammered. Adds `cert-manager.io/certificates`
  get/list/watch/delete to the control-plane ClusterRole (delete only). Unit
  test `TestCertStuck`. **Verified:** fresh tenant `beta` reached Ready fully
  hands-off — the reconciler auto-reset `tenant-beta-issuer-tls` 3× (~22s apart)
  until DNS warmed, then issued; `https://stub.beta.peristera.app` served a real
  cert. Off-boarding then GC'd the cross-namespace issuer ingress + cert in
  `peristera-system` with zero leftovers (cluster-scoped-owner GC works).
- **metrics-server disabled on cloud k3s (#42).** A wedged `tenant-test`
  namespace (stuck `Terminating` 72 min) traced to the same dev issue: under
  Cilium, k3s's metrics-server can't scrape the kubelet, its `metrics.k8s.io`
  aggregated API stays unavailable, and namespace GC (which blocks on
  aggregated-API discovery) stalls every deletion. The cloud cloud-init missed
  the `--disable=metrics-server` dev already uses. Fixed in `instance.tf` +
  disabled on the running node (k3s config).

Filed #53 (post-M7 tenant dashboard for self-service user management — the
current gap: a tenant's auto-created `admin` is a plain org member, no native
surface to add users). **M7 s2 fully verified** (tenant on real per-host TLS,
hands-off provisioning, clean off-boarding). PR #51.

## 2026-07-07 — Tenant user creation from the control plane

Replaced the silent auto-provisioned `initial-admin` Secret (operator had to
`kubectl` the credentials out — not sustainable) with an explicit operator
action, at the founder's request. Branch `m7-tenant-users`.

- **`POST /api/v1/tenants/{slug}/users {email}`** creates a human user
  (login = email) as `ORG_OWNER` in the tenant's own Zitadel instance and
  returns a generated **one-time password** — the handover artifact, returned
  once and never stored. The same endpoint covers the first admin and
  lost-login recovery. Maps 404 (no tenant) / 422 (not provisioned) / 409
  (duplicate email).
- **zitadel**: `CreateHumanUser` (returns id) + `AddOrgMember` (grants
  `ORG_OWNER`, so the tenant admin can manage its own users in the tenant
  Zitadel console at `https://<tenant>/ui/console` — the interim before the
  tenant dashboard #53); `ErrUserExists`.
- **Reconciler**: no longer creates an admin or the `initial-admin` Secret.
- **CP UI**: an "Add admin" form on Ready rows renders the one-time password
  inline (HTMX). Shared `Server.createTenantUser` backs both API and UI.
- **Tests**: godog tenant-apps scenario rewritten (create a user via the API,
  assert credentials returned); unit tests for `genPassword` + email validation.

Rationale: user management is an operator surface, not a k8s Secret; a lost
login is just "create another user." Naturally pairs with the optional-domain
tenant-creation flow (s4). Verify: build branch image, create a user for `demo`
via the CP, log into `demo.peristera.app/ui/console` with it.

## 2026-07-07 — M7 s3: landing page (peristera.io)

A single static "what/why" page for the public marketing apex. Branch
`m7-s3-landing`.

- **`landing/index.html`** — self-contained (inline CSS, no build, no external
  assets): what Peristera is (open-source federated workplace suite for
  European SMEs), the suite (Kamara/Ergonomos/office/federation), why
  (sovereignty, no lock-in AGPL, per-tenant isolation), an honest
  early/build-in-public status, links to the repo.
- **Serving** (`deploy/scaleway/manifests/landing.yaml` + a `landing` step in
  bootstrap.sh): a tiny `nginx:alpine` in its own `landing` namespace serving
  the HTML from a ConfigMap (created from `landing/index.html` — no image to
  build), Service, and an Ingress on `peristera.io` + `www.` with a
  cert-manager per-host cert. `peristera.io` added to external-dns
  domainFilters (`LANDING_DOMAIN`, default peristera.io).
- Validated: `bash -n`, rendered manifests parse, no leftover placeholders.

**Live-verify gated on `peristera.io` being delegated to Scaleway DNS** (like
peristera.app) so external-dns/cert-manager can serve it on real TLS. Next:
s4 — custom-domain tenants (`peristera.lu`) + the optional-domain create flow.

## 2026-07-07 — M7 s4: custom-domain tenants (BYO apex)

The load-bearing step for real deployments (R79/R81): a tenant can run under its
own domain instead of `<slug>.peristera.app`. Branch `m7-s4-custom-domains`.

- **`Tenant.spec.domain`** (optional, immutable, FQDN-validated) — the custom
  apex. `ValidDomain` + CRD schema (pattern + `self == oldSelf`).
- **Reconciler**: `tenantDomain` returns `spec.domain` when set, else
  `<slug>.<base>`. Everything public-facing (OIDC issuer, app/office hosts,
  ingresses, the Zitadel instance's custom domain) already derives from
  `tenantDomain`, so the whole custom-domain flavour flows through one switch —
  issuer `https://peristera.lu`, apps `https://<app>.peristera.lu`, each with a
  per-host cert (s2 wiring).
- **CP create** takes the optional domain (OpenAPI `domain` + `CreateTenant`
  handler + the UI create form), validated as an FQDN.
- Tests: `TestValidDomain`, `TestTenantDomain` (custom vs default).

Onboarding a custom domain is a small **operator step** (delegate to Scaleway
DNS, add to external-dns `domainFilters`, and — if NAT-loopback fails — a
`coredns-custom` entry): documented in `deploy/scaleway/README.md`. The
automation (dynamic external-dns zones, in-cluster resolution) and
**domain-ownership verification** for self-serve BYO domains are filed as #56 —
today custom domains are operator-provisioned, so the operator vouches for
ownership.

**Live-verify (gated on `peristera.lu` delegated to Scaleway DNS):** create a
tenant with `domain=peristera.lu` and confirm its apps serve on
`https://<app>.peristera.lu`. This completes M7's tenant story.

## 2026-07-07 — M7 s3 live: peristera.io + hairpin confirmed

`peristera.io` delegated to Scaleway DNS (NS → scw.cloud); external-dns
published the apex + `www` → node. The landing cert then stalled on
cert-manager's **in-cluster HTTP-01 self-check timing out** — which
**definitively confirms Scaleway has no NAT hairpin** (the pod can't reach the
node's own Flexible IP), the exact reason the CoreDNS override exists for
`.app`. The `coredns-custom` manifest only covered `${DOMAIN}`, so the landing
apex wasn't routed internally. Fixed: added `${LANDING_DOMAIN}` (apex + a
subdomain regex) to the override. Applied live → cert issued in ~45s.
**`https://peristera.io` is live on a real Let's Encrypt cert** (200; `www`
too). Same lesson applies to custom-domain tenants (#56): each custom apex
needs a CoreDNS entry, not just DNS + a cert.

## 2026-07-07 — M7 backups: Postgres → Object Storage (R85)

The last substantive M7 gap. CNPG streams WAL + daily base backups to Scaleway
Object Storage (barman-cloud), so the platform identity DB and every tenant DB
are point-in-time recoverable. Branch `m7-backups`.

- **De-risked live first**: patched `zitadel-db` with `barmanObjectStore` →
  `ContinuousArchiving=True`, an on-demand backup completed, CNPG set
  `firstRecoverabilityPoint`/`lastSuccessfulBackup` (only set once base backup +
  WAL are in the store). Then the same on a tenant DB (`demo` → `tenants/demo`),
  also verified.
- **Platform** (`manifests/cnpg-zitadel.yaml`): `backup.barmanObjectStore`
  (creds = SCW keys in `scaleway-secret`) + a daily `ScheduledBackup`, 7-day
  retention.
- **Reconciler** (`internal/controller/backup.go`): when `BACKUP_BUCKET` is set
  (cloud), `ensureDatabase` adds the backup block to each tenant's CNPG cluster
  (`s3://<bucket>/tenants/<slug>`), creates the per-namespace `backup-s3`
  credentials Secret, and a daily `ScheduledBackup`. No-op in dev. Config via
  `BACKUP_BUCKET`/`BACKUP_ENDPOINT`/`BACKUP_S3_KEY_ID`/`BACKUP_S3_SECRET`
  (the last two from `scaleway-secret`). RBAC gains cnpg `scheduledbackups`.
  Tests `TestBarmanBackup`, `TestBackupsEnabled`.
- **bootstrap.sh**: reads the bucket from `tofu output -raw backups_bucket`,
  pins the **CNPG chart to 1.30** (in-tree barman is removed in 1.31).

Follow-ups (#59): **blob backup** rides on #21 (blobs → Object Storage; the
PVC is currently unbacked), and **migrate off in-tree barman** to the
barman-cloud plugin before CNPG 1.31. **M7 is now feature-complete** — remaining
is only the `peristera.lu` custom-domain live-verify (DNS-gated).

## 2026-07-07 — Correction: Scaleway hairpin works (BYO custom domains simplified)

Investigating the BYO-custom-domain design (#56) overturned an earlier
conclusion: **Scaleway hairpin works.** The Flexible IP is assigned locally on
the node (`ens2` `51.15.210.70/32`), so a pod reaching it is a local hop, not an
edge-NAT loopback. Verified from a pod: `--resolve <custom>:80:51.15.210.70` →
404 (reached Traefik), and `cp.peristera.app` via the node IP **bypassing
CoreDNS** → 200. The s3 landing cert failure previously blamed on hairpin was
actually the incomplete `.io` NS delegation (resolving to the EuroDNS parking
IP). Consequences: the `coredns-custom` override is redundant (harmless), and
**BYO custom domains need no special in-cluster handling** — the tenant just
points A/CNAME at our IP (no delegation), and s4's ingress + HTTP-01 wiring
issues certs. #56 rescoped accordingly (stable CNAME target + ownership
verification are what actually remain).

## 2026-07-08 — M7 s4 verified live: peristera.lu custom domain, no delegation

The BYO-custom-domain acceptance, dogfooded on the real flow. Deployed `main`
(s4 + backups) to the cloud, applied the updated CRD, and provisioned a tenant
`{slug: lu, domain: peristera.lu}`. The founder set only two records at EuroDNS
(the registrar — **NS never delegated to Scaleway**): `peristera.lu A →
51.15.210.70` (apex) and `*.peristera.lu A → 51.15.210.70` (app hosts).

Result: **Ready in 60s**, `IAMProvisioned=True` (issuer `https://peristera.lu`),
apps up, **zero cert self-heals** (certs issued first try — hairpin works, DNS
was warm). Verified: `https://peristera.lu/.well-known/openid-configuration` →
200 (real LE cert `CN=peristera.lu`); `https://stub.peristera.lu` → 200;
`https://kamara.peristera.lu` → 302 /auth/login; all on real per-host certs;
`peristera.lu` NS still `ns1.eurodns.com`. This proves BYO custom domains need
only the tenant's A/CNAME → our IP — no delegation, no CoreDNS, no DNAT — and
that s4's existing reconciler wiring handles it unchanged.

**M7 COMPLETE.** Public platform + landing (peristera.io) + operator-provisioned
tenants on real TLS (default and custom domains) + tenant users + backups, all
live on Scaleway. Remaining follow-ups are tracked issues (#56 stable-CNAME
target + ownership verification, #59 blob backup + barman plugin, #53 tenant
dashboard).

## 2026-07-11 — Post-M7 batch kickoff: decisions of record (R90–R96)

A five-perspective audit (security, architecture, code quality, ops, UX/DX) ran
after M7 and filed 25 issues (#65–#89, security individually, the rest grouped +
cross-linked). Q&A Round 14 (R90–R96) then settled the fix batch; answers are in
`Q&A.md`, the plan in `docs/post-m7-plan.md`. Headline decisions: one DNS-01
wildcard cert story for platform *and* custom domains (custom via
`_acme-challenge` CNAME delegation), issuer/vanity-domain decoupling (making
`spec.domain` a reversible routing attribute), operator-initiated domain
ownership verification, a scoped optional-app reconcile that deletes on disable,
and office hardening.

First PR of the batch (this one) records the decisions and **accepts + documents
the shared-ingress host-header bounce** (R92): no L7 fix now, revisit with the
zero-trust/token layer. Recorded as an ADR-0016 amendment; #43 closed. The code
PRs follow per `docs/post-m7-plan.md` (office hardening → optional-app lifecycle
→ cert model + custom domains), each with security + code review, cloud-infra
verified live per the R96 sequence.

## 2026-07-11 — Office hardening (R95, #48 + #66)

Hardened the Collabora engine per R95's four calls. The admin console is
**disabled** (`--o:admin_console.enable=false`) and the hardcoded `admin/admin`
env is dropped — closes the exposure filed as #66 (admin console reachable on
the prod path with default creds). Prod-shaped `ssl.termination` is now **gated
on `tlsEnabled()`** (the single dev/prod switch): on the cloud coolwsd runs
behind TLS-terminating Traefik and emits https URLs; dev stays plain http. Added
a pod-level **RuntimeDefault seccomp** profile; the jailing capabilities
(incl. `SYS_ADMIN`) are retained since the moby default profile still permits
mount/chroot/mknod for a capability holder — accepted within the per-tenant
namespace (dropping them disables jailing, which is worse). The WOPI-token /
Traefik-accesslog stance is documented (no code; accesslog is off by default).

Refactored the Deployment into a pure `officeDeployment` builder (mirroring
`issuerIngress`) with a unit test (`office_test.go`) asserting the hardening —
first unit coverage for the office path. **Needs a live smoke test:** open a
document against a running office to confirm RuntimeDefault seccomp doesn't
break coolwsd's jailing (the R96 cloud-infra verification step).

Security + code-quality review (two agents): sound, no new vuln — confirmed that
`admin_console.enable=false` is what actually closes the console (removing creds
alone would not have) and that RuntimeDefault permits the cap-gated jailing
syscalls. Folded in the review nits (assert the WOPI allow-list and the full cap
set; `strconv.FormatBool`). Dropping the container's default cap set
(`Drop: ["ALL"]`) is a pre-existing least-privilege miss, out of R95 scope and
needing the same coolwsd smoke test — tracked as #95.
