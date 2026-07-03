# M2 plan — control-plane skeleton

- **Size:** ≤ 6 weekends (sizing rule, README §5). Honest warning: with a
  Kubernetes controller in scope this is *tight* — the abort valve is
  cutting polish, never the vertical slice.
- **Status:** parameters settled (`Q&A.md` Round 5, 2026-07-03); starts
  after M1 closes (ADR-0006 accepted).
- **Lifecycle:** working document, superseded by the M2 ADRs and worklog.

## Goal

The tenant lifecycle exists as a product: from a minimal HTMX UI, an
operator creates a tenant and gets an isolated, working stack — namespace,
dedicated Postgres, Zitadel virtual instance on its own domain, one app pod
— and can delete it again, cleanly. This is the MSP product's seed and the
platform-endgame's first cell (README §4).

## What M1 already proved (build on, don't re-verify)

- Zitadel virtual instances provision per tenant via the System API
  (cert-JWT, `SYSTEM_OWNER`), each with its own domain and OIDC issuer;
  marginal footprint ≈ 0. Deletion works (mind projection lag).
- CNPG-managed Postgres integrates cleanly (DSN mode).
- k3d + Traefik + wildcard tenant domains (`*.127.0.0.1.sslip.io`) work
  locally; ports 9080/9443.
- An OIDC relying-party stub in Go + HTMX exists in `iam/` (M1 session 3)
  — the login pattern every app copies, and M2's candidate app pod.

## Definition of done

From the control-plane UI, logged in via OIDC (default Zitadel instance):

- [ ] **Create tenant** → namespace, CNPG Postgres, Zitadel virtual
      instance on `<tenant>.<base-domain>`, app pod deployed and reachable
      on the tenant domain.
- [ ] Visit the tenant domain, **log in as a tenant user** end to end.
- [ ] **Delete tenant** → namespace, database, and Zitadel instance gone —
      off-boarding is GDPR posture and is in scope from the skeleton.
- [ ] Tenant list/detail UI shows real state (not a wishful database row).
- [ ] godog `.feature` specs drive the tenant lifecycle (working agreement
      #2); Go build + test CI runs on every push.
- [ ] ADRs accepted: URL/permalink + API-versioning conventions (deferred
      from M0 — **before the first endpoint ships**), control-plane
      architecture (see Round 5).
- [ ] `control-plane/` seeded (README, legal files); worklog; README §4/§5
      updated.

Demoable artifact: screen recording — create tenant, log in on its domain,
delete it.

## Shape (settled, Q&A Round 5)

1. **A `Tenant` CRD + controller (controller-runtime), not imperative
   client-go in HTTP handlers.** Reconciliation *is* this product —
   upgrades, staging clones, quotas are all "converge reality to spec"
   problems. Starting imperative means rebuilding on the operator model
   later. Named cost: k8s controllers are a documented LLM thin spot
   (ADR-0002) — mitigate with a `guidelines/` entry written as we learn,
   and no kubebuilder ceremony beyond what we use. **Decision rider (R23):
   revisit this architecture after M6** — check with real experience
   whether the controller pulls its weight; goes into the control-plane
   architecture ADR as an explicit review point.
2. **Tenant CRs are the source of truth; the control plane gets no
   Postgres in M2.** The tenant list is `kubectl get tenants` with a nice
   face. A control-plane database arrives when billing/quotas need one
   (2027), not before.
3. **IAM provisioning is part of tenant creation.** The System API seam is
   proven; a tenant without login is not a vertical slice.
4. **The app pod is the M1 stub relying party** — first entry of a
   *hardcoded* catalog (a Go slice, not a config surface; the catalog
   becomes data when a second app exists).
5. **Control-plane admin auth from day one** via OIDC against the default
   Zitadel instance — copy the M1 stub pattern; auth is the dependency of
   everything.
6. **Tenant domains are permanent.** The domain carries the OIDC issuer
   (M1 day-one rule), so the tenant slug is immutable at creation; display
   names can change freely. This folds into the permalink ADR: object
   identity = stable ID, never a name.

## Session schedule (indicative, 6 weekends)

| Session | Work |
|---|---|
| 1 | ✅ ADRs first: URL/permalink + API-versioning conventions; control-plane architecture (CRD + controller, CR as source of truth). Go CI attaches |
| 2 | ✅ `Tenant` CRD + controller: namespace + CNPG cluster reconcile, delete/finalizers |
| 3 | godog tenant-lifecycle spec first (red), then Zitadel provisioning in reconcile (green) — detailed plan below |
| 4 | App-pod deployment from the hardcoded catalog; tenant reachable end to end |
| 5 | HTMX UI + **OpenAPI-first** `/api/v1` (spec before handlers, oapi-codegen); login, tenant list/create/delete; godog suite into CI (k3d action) |
| 6 | Buffer + writing: worklog, README updates, guidelines entry on controllers, demo recording |

## Session 3 — detailed plan (2026-07-03)

**Goal:** `kubectl apply` a Tenant and get a *log-in-able* tenant — the
reconcile loop performs the full ADR-0006 §6 IAM sequence and reports
`issuer` + `clientId` in status; deletion removes the Zitadel instance.

**Spec first — godog starts here.** Honest accounting: sessions 1–2
shipped without `.feature` specs, a deviation from working agreement #2
(M1 was a spike; the controller no longer is). Not too early — the tenant
lifecycle is exactly the domain/API level the agreement means:

- `control-plane/features/tenant_lifecycle.feature`, scenarios:
  *provisioning* (create → phase Ready, namespace exists, OIDC discovery
  answers on the tenant issuer), *off-boarding* (delete → namespace gone,
  discovery 404s), *slug immutability* (update rejected by the API
  server).
- Steps drive the real dev cluster (k3d + CNPG + Zitadel), guarded by
  `PERISTERA_E2E=1` so plain `go test` and CI stay green without a
  cluster. The suite joins CI in session 5, when the controller is
  containerized and CI gets a k3d cluster.

**Implementation steps (red → green):**

1. `internal/zitadel` client: self-signed RS256 system JWT (private key
   from a file path in dev, a Secret in-cluster — the key must move from
   the workstation into the `admin-client-tls` Secret alongside the cert);
   CreateInstance, GetInstance, DeleteInstance, AddTrustedDomain,
   CreateProject, CreateOIDCApp. Audience is always the deployment issuer
   (ADR-0006 §5). Promotion to `lib/` waits for a second consumer.
2. Reconcile, after the database: ensure instance — `status.instanceId`
   is the idempotency record (CreateInstance is not idempotent; if status
   was lost, adopt by custom-domain search) — then trusted domain,
   project, PKCE app (`idTokenUserinfoAssertion: true`), then
   `status.issuer` + `status.clientId`. Phase Ready = DatabaseReady ∧
   IAMProvisioned (as conditions).
3. Finalizer: delete the instance by `status.instanceId`, requeueing
   through the few-seconds projection lag, then let owner-ref GC take the
   namespace.
4. Controller config via env (ConfigMap when containerized):
   `ZITADEL_BASE_URL`, `TENANT_BASE_DOMAIN` (`127.0.0.1.sslip.io`),
   `TENANT_EXTERNAL_PORT` (`9080`), `SYSTEM_USER_KEY` (path).
5. **App URL convention**, needed now for redirect URIs, served in
   session 4: apps live at `<app>.<slug>.<base-domain>`
   (`stub.demo2.127.0.0.1.sslip.io`). Kubernetes ingress wildcards match a
   single label, so `*.127.0.0.1.sslip.io` (→ Zitadel) can never shadow
   the two-label app hosts — per-app ingress rules stay conflict-free.
   Redirect URI registered at provisioning:
   `http://stub.<slug>.<base>:<port>/auth/callback`.

**Answered along the way (Q&A-in-chat, 2026-07-03):**

- **godog:** adopted from session 3, spec-first; not earlier ceremony but
  the actual dev loop from here on.
- **OpenAPI:** no specs before endpoints exist. The Tenant CRD schema is
  generated and authoritative for the tenant shape; the first HTTP API
  (`/api/v1/tenants`, session 5) is written OpenAPI-first with generated
  server stubs (oapi-codegen), per the API-first principle.

## Session 5 — detailed plan (2026-07-03; starts after session 4)

**Goal:** the control plane becomes a product surface. An operator opens
`http://cp.<base-domain>:9080`, logs in via OIDC, and creates/watches/
deletes tenants in the browser — with everything the UI can do also being
a documented `/api/v1` endpoint, and the godog suite running in CI.

**Sizing honesty:** auth + API + UI + containerization + CI is the fattest
session of M2. Session 6 is the buffer it may spill into; the abort valve
(one ugly page over a working slice) applies to the UI, never the API or
the CI gate.

1. **Spec first, twice.**
   - New godog scenarios (API level — this is where godog shines; browser
     login stays a playwright script like M1): *create a tenant via
     `POST /api/v1/tenants`*, *list shows phase and issuer*, *delete via
     API off-boards*, *unauthenticated requests are rejected*.
   - `control-plane/api/openapi.yaml` (OpenAPI 3.0) before any handler:
     tenants CRUD, schemas mirroring the CRD (spec: slug, displayName;
     status: phase, issuer, clientId), error shape. Server stubs + types
     generated with **oapi-codegen** (std-lib server, `go:generate`;
     tooling choice recorded here, promoted to `guidelines/` when a
     second service adopts it).
   - **Identity note (no ADR-0007 conflict):** the tenant slug *is* the
     permanent identifier (ADR-0007 §4 carves out exactly this), so
     `/api/v1/tenants/{slug}` is a stable URL. UUIDv7 object IDs start
     with the first *app* objects (M3).
2. **One binary.** The HTTP server joins `cmd/controller` as a
   manager Runnable — one process, one pod, one deployment ("the control
   plane"). HTMX fragments and `/api/v1` JSON share the same domain
   functions; the UI is the first API client in spirit, not via loopback
   HTTP.
3. **Operator auth.** OIDC auth-code + PKCE against the **default**
   Zitadel instance (M1 stub pattern hardened into middleware guarding
   both UI and API; in-memory sessions, accepted M2 limitation — the
   shared session convention is an ADR before M3). Bootstrap
   chicken-and-egg: at startup the control plane **ensures its own OIDC
   app** in the default instance via the system client (idempotent,
   same code path as tenant provisioning); the first operator user is
   created by a documented one-time call (product-managed operators come
   later).
4. **HTMX UI.** Tenant list (slug, phase, issuer link), create form,
   delete-with-confirm; rows poll while Pending so Ready appears live —
   that is the demo moment. Discipline from the first template: no
   hardcoded strings (message catalog, EN content only for now — FR/DE/LB
   are target locales), semantic HTML; the a11y CI gate formally attaches
   at M3 per the M0 deferral list.
5. **In-cluster.** Multi-stage Dockerfile (distroless), `k3d image
   import` (no registry), manifests in `control-plane/deploy/`:
   Deployment in `peristera-system` mounting `admin-client-tls`,
   ServiceAccount + least-privilege RBAC (tenants + status, namespaces,
   CNPG clusters, secrets read), ingress at `cp.<base-domain>`.
6. **godog into CI.** New workflow job: k3d action → CNPG + Zitadel
   (fresh keypair generated in CI — it is per-deployment config, not a
   shared secret; sslip.io resolves to the runner's localhost) → deploy
   the control plane image → `PERISTERA_E2E=1` suite. Expected ~5–6 min;
   if it drags, fallback is control-plane-paths + nightly, decided by
   evidence not upfront.

**DoD:** operator logs in and manages tenants in the browser;
`openapi.yaml` + generated stubs committed and handlers pass the new API
scenarios; control plane runs in-cluster with least-privilege RBAC; the
full godog suite is green locally **and in CI**; worklog + READMEs
updated.

**Abort valve:** if session 4 ends without a full create→login→delete
slice, cut the UI to a single ugly page and ship the slice — broad and
shallow beats narrow and deep (Principle 4).

## Out of scope (deferred, not dropped)

- Quotas, billing, upgrade flows, staging clones (2027 control-plane
  alpha).
- Break-out of a tenant to a dedicated Zitadel (designed in M1; built when
  a real tenant needs it).
- OpenFGA, audit events, personal-data metadata, search (attach before
  M3/M4 store user data — tenant metadata in M2 is operator data).
- Multi-cluster, the k3s one-command installer, Scaleway.
- Any configuration surface: one opinionated tenant shape, no options.
