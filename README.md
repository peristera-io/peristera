# Peristera

**Peristera is an open-source, federated workplace platform for European SMEs — a
Microsoft 365 alternative you can self-host, buy from a local MSP, or use as
SaaS, built so that working *across* organizations is as easy as working inside
one.**

*Peristera* (Greek περιστέρα, "dove") — the bird that carries messages between
places that don't share a roof. That is the product thesis in one image.

> **How to read this file.** This README is the project's strategy document and
> the primary context source for LLM-assisted development. It records what we
> are building, for whom, in what order, and which decisions are settled vs.
> open. Start every working session (human or LLM) by reading it. Decisions
> made in dialogue are archived in `Q&A.md`; durable decisions get an ADR in
> `adr/`.

## Current status — updated 2026-07-06

**M0–M2 complete.** M0: the monorepo at `github.com/peristera-io/peristera`
(`LICENSE` AGPL-3.0, repo-wide CLA + per-project templates, bootstrap ADRs
0001–0005, markdown/link CI, CLA bot). M1 (closed 2026-07-03, ADR-0006):
Zitadel integration confirmed — one shared deployment, one virtual instance
per tenant. **M2 (2026-07-04): the tenant lifecycle is a product.** From the
HTMX UI (operator OIDC login) or `/api/v1` (OpenAPI-first): create a tenant
→ namespace + Postgres + Zitadel virtual instance + app pod + initial-admin
credentials, ~25 s to Ready; log in on the tenant's own app; delete it
cleanly. `Tenant` CRD + controller (ADR-0007/0008), godog suite (7
scenarios) drives the dev loop and the e2e CI job (`hack/dev-cluster.sh`
brings up the full environment). A five-agent review closed M2 out; the
non-blocking findings are tracked as GitHub issues.

`iam/`, `control-plane/`, `lib/`, `ergonomos/`, and `kamara/` now exist on
disk; further project folders appear with their first code, per §8.

**M3 complete (2026-07-04): Ergonomos, the first app that stores user
data, wired through the GDPR-by-design spine.** ADRs 0009–0014
(personal-data metadata, OpenFGA conventions, audit events, search feed,
catalog contract, goose migrations), built as the MIT `lib/` module
(`id`, `pii`, `authz`, `audit`, `search`, `oidcrp`, `session`). Ergonomos
is a single-user task app deployed per tenant with its own database +
OpenFGA; every task mutation flows through personal-data metadata (export/
erase), authorization (OpenFGA owner tuples, permission-filtered lists),
audit (pseudonymized actor), and the search feed — verified live in the
tenant database. Accessibility gate (axe-core, WCAG 2.1 AA) in CI. Each
session fresh-context-reviewed.

**M4 complete (2026-07-06): Kamara, the per-tenant file store.** Files are
content-defined, deduplicated, at-rest-encrypted chunks — FastCDC chunking,
BLAKE3 content-addressing, XChaCha20-Poly1305 under a per-tenant DEK,
ref-counted GC — behind a bearer **storage API** and a browser **file UI**.
A folder hierarchy with OpenFGA `can_access` inheritance; create / upload /
rename / move / delete via cookie-authed HTMX; a drag-drop uploader
component with a progress bar and a file-details drawer. Deployed as a
catalog app (first stateful-beyond-Postgres app: per-tenant blob PVC +
per-tenant DEK Secret), wired through all four conventions. Tailwind is the
design-language pilot; the a11y gate runs across four UI states. **Six
adversarial reviews**, each triaged; an end-to-end Playwright demo (login →
browse → upload → download) is the acceptance artifact. The **inter-service
auth model was deliberately deferred** to its own design milestone rather
than settled to make one test pass — the Ergonomos file-attach flow is that
milestone's acceptance test (Q&A R41, `docs/m5-plan.md`, #29).
ADRs: root 0015 (transactional storage); Kamara 0001 (chunk format), 0002
(folder hierarchy). Next: **M5 — service-to-service auth / intra-tenant
zero-trust** (#29, `docs/m5-plan.md`, Q&A R10), then OnlyOffice (M6) and the
SaaS/Scaleway public demo (M7).

*Update this block whenever reality changes — a stale status line is exactly
the rot §8 warns against.*

---

## 1. Mission and north star

**Mission (now):** give European SMEs a workplace suite — files, tasks,
documents, calendar, later messaging — that is open source, opinionated,
federated, and easy for a local MSP to sell and operate. Keep the data, the
money, and the control in Europe.

**North star (long horizon):** Peristera's delivery mechanism is a
self-provisioning application platform on Kubernetes. If the suite succeeds,
that platform — a curated catalog of sovereign business applications, deployed
per tenant with one click — grows toward a European alternative to Azure-style
managed platforms. We do **not** market this yet; a solo project claiming to
replace Azure has no credibility. We *architect* for it from day one.

**Strategic frame (decided):** *suite-first messaging, platform-first
architecture.* Every public message is about the suite ("the federated
SharePoint/OneDrive alternative"). Every architectural decision (control
plane, namespace-per-tenant, app catalog) is made as if the platform is the
endgame.

## 2. Market strategy

### The problem

European SMEs run on Microsoft 365 by default. The consequences: recurring
per-seat cost flowing out of the local economy, data governed by non-EU law,
no meaningful alternative that an SME's local IT partner can actually sell,
and — even inside the Microsoft world — data silos between organizations.
Cross-company collaboration today means emailing attachments or granting guest
accounts, both of which are bad.

Existing open alternatives don't close the gap: Nextcloud is powerful but
suffers option-deluge and an aging UX; openDesk targets public administration,
not SMEs-via-MSPs; Seafile/OnlyOffice are point solutions. None of them make
**cross-organization collaboration** a first-class primitive, and none of them
make life easy for the MSP who has to run twenty instances.

### Positioning — four angles

1. **Opinionated defaults, no option deluge.** It works well out of the box or
   it doesn't ship. Configuration is a last resort, not a feature.
2. **No data silos.** Peristera instances federate: identity, tasks, documents,
   files, calendar entries flow between organizations (and between your
   personal instance and your employer's) without exports, guest accounts, or
   "something complicated". This is the heart of the project and the moat.
3. **Easy for MSPs to sell.** A turnkey control plane: new customer → new
   isolated tenant (own namespace, own database) in one click, with quotas and
   billing built in. The MSP's margin is our go-to-market.
4. **GDPR built in, not bolted on.** Subject-access export, erasure, and a
   live registry of what personal data is stored where are one-click
   operations across the whole suite. For an SME, answering a GDPR request is
   real pain today; for an MSP, "compliance included" is a selling point
   Microsoft answers only with enterprise-priced tooling.

### Sequence of attack (the wedge)

1. **SharePoint + OneDrive replacement** (Ergonomos + Kamara), coexisting
   peacefully with Outlook/Teams. This is the beachhead — and because a file
   hub without document editing just recreates the email-attachment problem,
   the beachhead includes **document co-editing via an OnlyOffice
   integration** (M6), showcased in the first public demo (M7).
2. **Teams alternative** (messaging/meetings) once the beachhead holds.
3. **Office/document editing as our own product** last — it is the hardest
   and least differentiated fight, and may never be worth fighting if the
   OnlyOffice integration holds.

**The bundle problem, said out loud.** An SME on Microsoft 365 gets SharePoint
and OneDrive at zero marginal cost — the wedge attacks the *free* part of the
incumbent's bundle. We do not win on price against "included". We win on
sovereignty, on federation (which M365 structurally cannot do), on the MSP
relationship, and on the endgame of replacing the whole bundle — at which
point the entire M365 subscription becomes the price comparison. Every MSP
will raise this; the answer belongs in every sales conversation.

### Go-to-market phases

| Phase | Audience | Channel | Goal |
|---|---|---|---|
| **0 — now → ~mid-2027** | Self-hosting community | Build in public: public demo instance, blog/posts, repo | Feedback, credibility, early contributors |
| **1** | SME early adopters | Own SaaS (hosted on **Scaleway** — the sovereignty story requires an EU provider) | Revenue signal, operational learning |
| **2** | MSPs / integrators | Direct outreach + certification program | Scalable distribution |

**Front door:** GitHub org **`peristera-io`** (code + issues), email
**<peristera@peristera.io>**. More channels only when an audience demands
them.

Conference talks (FOSDEM etc.) once there is something running to show.
**Grants** (NLnet/NGI Zero, Luxinnovation, etc.): deliberately deferred until
there is a public demo — apply from a position of "look what exists", not
"trust the plan".

### Business model

- **Year 1 (2026–27):** no revenue. Build, showcase, build support for the
  cause. Everything public.
- **Years 2–3:** (a) hosted SaaS, (b) MSP program — certification, support
  contracts, turnkey licensing of commercial add-ons.
- The CLA's relicensing clause deliberately keeps **open-core / dual-licensing
  optionality** for later. We say this openly rather than letting the
  community discover it.
- **Pricing hypothesis (straw man, to validate in Phase 1/2): no per-seat
  pricing.** Per-seat is the M365 model and everyone resents it — refusing it
  also sidesteps a head-on price comparison with the "free" parts of the
  bundle. SaaS is **usage-based plus a flat admin fee (~€15/tenant/month)**;
  MSPs pay for **certification and priority support, scaled by tenant
  count**, and set their own retail.

## 3. Product principles (decision rules)

Use these to settle design arguments. If a proposal violates one, it needs an
ADR explaining why.

1. **Opinionated defaults.** Every new configuration option must justify its
   existence. Prefer conventions over settings.
2. **Federation is the product.** Design every feature for the cross-instance
   case first; single-instance is the degenerate case, not the other way
   around.
3. **Built for normal users** — and the MSP admin is also a user. The control
   plane deserves the same UX care as the apps.
4. **Everything ships as a vertical slice.** All components evolve together as
   minimal-but-working stubs. Broad and shallow beats narrow and deep: it
   keeps opinions cheap to revise. Multi-user working beats single-user
   bells-and-whistles.
5. **One controlled environment.** One deployment contract (Kubernetes), one
   database (Postgres), one backend language (Go). Every exception is an ADR.
6. **Great documentation, generated from this repo.** If it isn't written
   down, it didn't happen — this is also what makes LLM-assisted development
   work.
7. **Fast is a feature — with a budget and a test.** Working target: p95
   server response under ~200 ms for common interactions — a measurable
   proxy; the real goal is *perceived* speed, and concrete per-interaction
   budgets live in the ADR backlog. Perceived bloat is the top complaint
   about both M365 and Nextcloud; the stack is the right bet, but only a
   budget holds the line. Because this is easy to lose and
   hard to eyeball, automated performance checks run in CI from early on —
   the tests carry the discipline, and we revisit this principle often.
8. **GDPR is a design constraint, not a feature.** Every entity that can hold
   personal data declares it in metadata at the schema level. Export and
   erasure are first-class operations in every app from the first stub — not
   batch scripts written under deadline when the first request arrives. A new
   data model that can't answer "which person does this relate to, how do I
   export it, how do I delete it" doesn't ship. The same metadata answers the
   opposite legal duty: what must be *kept* (retention classes, legal holds).

## 4. Architecture

### Platform shape

- **Kubernetes is the only deployment contract.** No docker-compose support
  matrix. Two documented paths: (a) *one VM* via our one-command **k3s
  installer** (keeps the self-hosting community on board), (b) *bring your own
  cluster* (MSPs).
- **Control plane** ("admin dashboard" grown up): the tenant lifecycle
  manager. New customer → new **namespace**, own Postgres, app pods deployed
  from a **curated catalog**. Quotas, billing, upgrades, backups live here.
  This is the MSP product and the platform seed. Its signature move:
  **one-click staging environments** — clone a tenant's whole production to a
  staging namespace, test a difficult upgrade there, then promote.
  De-risking upgrades is a product feature, not an ops runbook. (Note: a
  clone carries the same personal data as prod — same controller, but
  retention and erasure must apply to staging too; see ADR backlog.)
- **Tenant isolation = namespace-per-tenant** with a dedicated Postgres per
  tenant (via the CloudNativePG operator). Clean blast radius, clean data
  separation, clean off-boarding.
- **Curated catalog only** for the first ~2 years: Peristera apps + the IAM
  engine + OpenFGA + OnlyOffice + the Postgres operator. Not arbitrary
  workloads. Third-party
  components are not a loss of control — we control the *experience* by
  controlling deployment, configuration, upgrades, and backups.

### Federation model (the heart)

- **The scenario that must work:** Anna's fiduciary runs Peristera at their
  MSP; Ben's engineering office runs its own instance. Anna shares a project
  with Ben — he sees its tasks and files inside *his* Peristera, works with
  his own identity, and the audit trail on both sides shows exactly what
  crossed the boundary. No guest account, no attachment, no third silo.
- **Peristera↔Peristera only.** We define one small, signed, HTTP-based
  protocol between instances. The design ADR surveys ActivityPub and Matrix,
  but per our build-vs-buy default (below) we expect to build our own.
  Identity federation rides on OIDC between instances. The protocol carries
  identity assertions across trust boundaries, so the ADR includes an
  explicit threat model (see ADR backlog).
- **Trust model v1: allowlist-only (decided).** Instances federate only with
  explicitly trusted peers — key exchange arranged by admins or by the MSP.
  This kills the malicious-peer problem for v1 and matches the MSP topology;
  open discovery is a later decision, not a v1 burden.
- **The cold-start answer.** Federation only helps if the other side runs
  Peristera too — the network-effect problem that keeps email attachments
  alive. Two structural answers: (1) **personal↔employer federation needs no
  second company** — your private instance federating with your employer's
  (the merged calendar) pays off inside a single rollout; (2) **every MSP is
  a network seed** — an MSP hosting twenty tenants creates twenty federable
  organizations that already share an operator.
- **Sequencing, reconciled with Principle 2:** data models are
  federation-*ready* from the first stub (stable object IDs,
  instance-namespaced subjects, OpenFGA relations that can point at remote
  users); federation is *delivered* in 2027. Cross-instance-first design,
  single-instance shipping.
- **Third-party systems connect via adapters, not federation.** Example: a
  CalDAV ingest adapter lets your personal Google calendar appear alongside
  your corporate Peristera calendar. You keep your existing tools and still
  get the merged view.
- First federated objects: identity, then tasks/calendar entries, then
  documents and files.

### GDPR by design (cross-cutting)

Every Peristera app follows the same personal-data contract, defined once as a
shared convention (ADR before M3/M4 store their first byte, library in
`lib/`):

- **Data-subject metadata.** Schemas annotate which fields/objects can relate
  to a natural person. In Ergonomos, for example, content put into a task or
  document can be marked as relating to a person. The annotation lives at the
  data-model level so tooling can act on it generically.
- **Subject-access export.** Per-person, machine-readable export across all
  apps of a tenant, orchestrated by the control plane (which is also where a
  whole-tenant export for customer off-boarding lives — good GDPR posture and
  good MSP posture are the same feature).
- **Retention classes and legal holds — erasure's legal mirror.** The same
  metadata carries how long data must be *kept* (accounting records ~10 years
  in Luxembourg, contracts, employment documents) and whether a legal hold
  applies. Erasure refuses or defers what the law requires keeping. Without
  this, customers either break retention law or never dare to use erasure.
- **Erasure.** Deletion cascades follow the same metadata. If export can find
  it, erasure can remove it — with one deliberate exception, the append-only
  audit log (see hard edges below). For backups: per-tenant encryption keys (see
  conventions below) make whole-tenant erasure from immutable backups a key
  deletion (crypto-shredding); within-tenant subject erasure in backups is
  handled by a bounded backup-retention window, stated in the DPA.
- **Processing registry.** One click produces the table of what personal data
  is stored where — effectively the Article 30 records view.
- **Residency** falls out of the platform: EU hosting (Scaleway) for SaaS,
  self-hosting or local MSPs otherwise.
- **Known hard edges (early ADRs, not hand-waving):** erasure inside CRDT
  histories (CRDTs retain history by design — needs a tombstone/compaction
  strategy), erasure propagation across federation (data already shared to
  another instance), E2EE replicas (ciphertext you can't inspect but must
  still be able to drop), and the append-only audit log — audit events are
  themselves personal data (who did what, when) yet must stay tamper-evident;
  likely resolution is pseudonymized subjects with an erasable mapping,
  decided in the ADR.
- **Beyond GDPR (cheap now, mandatory later).** NIS2 pushes supply-chain
  security duties onto our MSP customers, and the Cyber Resilience Act will
  impose SBOM, security-update, and vulnerability-disclosure obligations on
  commercially distributed products (~2027; verify timeline when it bites) —
  so CI produces **SBOMs and signed releases before anything ships publicly
  (M7)** and the repo carries a **`SECURITY.md` disclosure policy**. The **EU Data Act**'s cloud-switching
  rules are answered by whole-tenant export — frame it that way in marketing.
  Watch-list, zero work now: **eIDAS 2 / EUDI wallet** as a future IdP
  integration behind Zitadel; **EN 16931 e-invoicing** when control-plane
  billing becomes real.

### Cross-cutting conventions (defined once, before first use — see the M0 attachment list — shared via `lib/`)

What every app inherits instead of inventing:

- **One permission model: OpenFGA** (decided). Zanzibar-style
  relationship-based access control — CNCF, written in Go, Postgres-backed,
  fits the stack. Each app contributes relations to one shared authorization
  model instead of growing its own ACLs, so "who can see this?" stays
  answerable in one click, across apps, forever — the SharePoint failure mode
  this exists to prevent. **One OpenFGA instance per tenant namespace, backed
  by the tenant's Postgres** — consistent with the isolation model, and
  tenant export/erasure naturally includes the permission tuples (which are
  personal data too). Design cares for the ADR: federated subjects (user IDs
  namespaced by home instance) and permission-filtered listings/search
  (`ListObjects` cost).
- **Audit events.** Every mutation emits a typed, per-tenant, append-only
  audit event. Enterprise ask #1, NIS2 evidence, MSP support tooling, and our
  own debugging — and impossible to retrofit, because old code paths never
  emit events.
- **Unified search.** Every app feeds a per-tenant search index (Postgres
  full-text first; no second search engine until it demonstrably breaks).
  Results are permission-filtered through OpenFGA. Cross-app search is the
  single most-missed feature in M365 — it only happens if it's a convention.
- **Per-tenant key hierarchy (crypto-shredding).** Tenant data and backups
  are encrypted under per-tenant keys, so tenant off-boarding and
  whole-tenant erasure from immutable backups reduce to key deletion. Also
  the foundation for Kamara's later E2EE federated replicas.
- **Permalinks that never break.** Object identity is a stable ID, never a
  path. URLs carry the ID; renames and moves leave working links behind.
  SharePoint's broken-link hell is one URL-design decision, made once, here.
- **API-first.** Everything the UI can do is a documented, versioned API —
  the HTMX UI is just the first client. Outbound events via webhooks (the
  audit-event stream provides them almost for free). This is also what makes
  the adapter strategy credible.
- **One notification service.** Apps emit notification events; a single
  service decides delivery. Opinionated default: digest-first and quiet.
  Fully user-configurable — each user tunes their whole setup in one place,
  not per-app toggles scattered across the suite. Designed in from the
  beginning, because unifying three apps' emails later never happens.
- **Undelete and version history everywhere.** A universal trash +
  versioning convention (the GDPR soft-delete machinery half-builds it
  anyway). "I deleted the wrong thing" is the #1 support-ticket generator;
  recovery is self-service.
- **Multilingual and accessible from the first template.** No hardcoded
  strings, ever; FR/DE/EN (and LB) as target locales. Semantic
  server-rendered HTML — HTMX makes this natural — with EN 301 549 / the
  European Accessibility Act as the bar and automated accessibility checks
  in CI. Both are agony to retrofit and near-free to start with.

### Build vs. buy — the default is build

We want to own the stack top to bottom; that is what "controlled experience"
and the platform endgame require. A third-party component earns its place
only by fitting *all* the constraints in this document: runs in the per-tenant
k8s catalog, Postgres-backed, readable (Go strongly preferred), configurable
down to opinionated defaults, exportable and erasable. Three exceptions
today, each with its role stated:

- **Zitadel — bootstrap, all-in (decided).** Security-critical maturity we
  cannot compress solo: auth bugs are security incidents, and SMEs arriving
  from Microsoft need the Entra ID/LDAP import machinery that took others a
  decade to harden. We commit fully rather than hedging behind an
  abstraction layer; if Zitadel fails us, the fallbacks (Ory, Keycloak) are
  worse fits — accepted as a named risk.
- **OpenFGA — permanent.** Not a compromise: it is genuinely good, fits every
  constraint, and relationship-based access control is exactly our domain.
- **OnlyOffice — bootstrap.** Document co-editing must exist at the first
  public demo, and an editor suite is a decade of work. Integrated, not
  absorbed; building our own stays the last fight, if it ever happens.

Everything else — federation protocol, sync engine, control plane,
collaboration engine — we build.

### Components

| Component | What it is | Build strategy |
|---|---|---|
| **Control plane** | Tenant lifecycle, catalog, quotas, billing | Own code, Go + HTMX |
| **Peristera IAM** | Login, users, OIDC for all apps | **Branded layer over Zitadel — decided, all-in** (see build vs. buy). Integration confirmed in M1: one shared deployment, one virtual instance per tenant, domain per tenant, break-out seam designed in — ADR-0006. |
| **Peristera Ergonomos** | Tasks/projects → collaboration engine ("functionality of SharePoint, UI of Notion"); emits calendar entries | Own code. HTMX first; the Notion-like block editor will need Svelte islands + a CRDT (library choice is an open ADR) — planned, not improvised |
| **Peristera Kamara** (Greek καμάρα, "vault" — renamed to avoid the HashiCorp Vault collision) | File storage: chunked upload first, then sync | Own code, clean restart informed by an earlier Go encrypted-sync prototype. Exposes a **storage API consumed by the other apps** as their file layer. E2EE comes later and enables *federated encrypted replicas* (I store my friend's ciphertext, he stores mine) |
| **Documents** | Office document co-editing inside Ergonomos/Kamara | **OnlyOffice integration** (bootstrap — see build vs. buy). Own editor: last fight, if ever |
| **Messaging** (name TBD) | Teams alternative | Phase 2, not designed yet |

### Stack (settled — deviations need an ADR)

| Layer | Choice | Rationale / LLM notes |
|---|---|---|
| Backend | **Go** | Single binaries, the entire k8s ecosystem is Go, best-in-class LLM training coverage |
| Web UI | **HTMX** (server-rendered), **Svelte islands** only where the interaction model demands it (Ergonomos editor) | HTMX keeps complexity server-side where Go lives; both well covered in LLM training data |
| On-device apps | **Flutter** (mobile). Flutter Web is weak — browser stays HTMX/Svelte. Kamara desktop client: possibly Go + native shell, open | |
| Database | **PostgreSQL** only, one per tenant, CloudNativePG operator | |
| Authorization | **OpenFGA**, one instance per tenant namespace, backed by the tenant's Postgres | Zanzibar-style ReBAC; CNCF, Go; one shared model across all apps |
| BDD tests | **godog** (official Cucumber for Go) | Gherkin `.feature` specs at domain/API level drive the dev loop |
| Deployment | **Kubernetes only**; k3s installer for single-VM | |
| LLM thin spots | CRDT internals, k8s operators/controllers | Compensate with detailed ADRs + `guidelines/` the LLM reads first |

**Explicit non-goals:** docker-compose as a supported target, a second
database, SPA-by-default frontends, building auth from scratch, generic
run-anything PaaS (yet).

## 5. Roadmap

**Sizing rule:** solo developer, nights and weekends, LLM-assisted. A
milestone that can't produce something demoable within ~6 weekends gets split
or cut. Dates are targets, not promises.

### Now → end of 2026 — "four actions on a public demo"

**Definition of done:** a stranger can visit the public demo instance
(Scaleway), **log in via Peristera IAM, manage tasks in Ergonomos, upload a
file to Kamara, and open + co-edit an office document — all in the browser.**
Built in public along the way.

- **M0 — Bootstrap, minimal** (a weekend, not a month): `git init` the
  monorepo, `LICENSE` in place, move the legal files into `templates/legal/`
  and instantiate per project (see §7), plain build-and-test CI, and only the
  ADRs M1 needs: monorepo, stack, k8s-only, build-vs-buy. Everything else
  attaches to the milestone that first needs it — **deferred, not dropped**:
  - permalink + API-versioning conventions → with **M2**, before the first
    URL/endpoint ships
  - personal-data metadata (incl. retention/legal holds), OpenFGA model,
    audit events, search feed → before **M3/M4** store their first byte
  - accessibility checks in CI → with **M3**, the first real UI
  - the **per-tenant key hierarchy / crypto-shredding** convention (README
    §4) → with the **backup / off-boarding story (~MSP alpha, 2027)**, which
    is where crypto-shredding and backup-erasure become real (issue #9)
  - SBOM generation, signed releases, `SECURITY.md` → with **M7**, before
    anything is public
- **M1 — IAM integration spike** *(first real work; Zitadel is decided,
  all-in)*: 2-week time-boxed **confirmatory** spike — Zitadel deployed on
  the dev cluster, one test user logs in to a stub page via OIDC, the
  integration approach written up as an ADR. It settles *how*, not
  *whether*.
- **M2 — Control-plane skeleton** *(done 2026-07-04)*: the tenant lifecycle
  as a product. From a minimal HTMX UI (operator OIDC login) or the
  OpenAPI-first `/api/v1`: create a tenant — namespace, dedicated Postgres,
  Zitadel virtual instance on its own domain (provisioned as part of tenant
  creation), one app pod, and generated initial-admin credentials — log in
  on it, delete it cleanly. `Tenant` CRD + controller (architecture
  revisited after the public demo); tenant CRs are the source of truth, no control-plane
  database until billing/quotas. Plan: `docs/m2-plan.md`.
- **M3 — Ergonomos stub**: single-user task lists, minimal but pleasant.
  (Multi-user matters more than single-user polish — it comes right after the
  demo, before any bells and whistles.)
- **M4 — Kamara stub** *(done 2026-07-06)*: chunked browser upload, storage
  API v0 that Ergonomos can call, folder hierarchy + browser UI. Design
  (living): `kamara/SPEC.md`; plan: `docs/m4-plan.md` / `docs/m4b-plan.md`;
  format decisions in `Q&A.md` Rounds 7–9. E2EE-ready chunk format, but sync
  and E2EE themselves are deferred.
- **M5 — Service-to-service auth / intra-tenant zero-trust**: one app calls
  another **on behalf of a user** under a real zero-trust posture — machine
  identity per app, RFC 8693 on-behalf-of tokens, local JWT validation,
  Cilium-enforced service-topology allowlist, actor-aware audit. Proven by a
  real Ergonomos → Kamara call. The platform S2S contract every future
  app-to-app interaction inherits — and a prerequisite for M6 (OnlyOffice ↔
  Kamara is itself an S2S call). Plan: `docs/m5-plan.md`; decisions in
  `Q&A.md` Round 10.
- **M6 — OnlyOffice integration**: open and co-edit a document stored in
  Kamara, in the browser. Without this, the file hub recreates the
  email-attachment problem it exists to kill.
- **M7 — Public demo**: deployed on Scaleway via the control plane itself
  (dogfooding), demo tenant, first build-in-public posts. Ships with the
  compliance CI (SBOM, signed releases, `SECURITY.md`).

### 2027 — federation + MSP alpha

- **Federation v1**: the Peristera federation protocol (ADR + reference
  implementation): identity first, then task/calendar sharing between two
  instances. *This is the flagship differentiator — treat it as a
  self-contained work package (which also makes it a clean grant
  application once the demo exists).*
- **Merged calendar** view + **CalDAV ingest adapter** (personal Google/iCloud
  calendar next to corporate entries).
- **Ergonomos multi-user** (this pulls in the Svelte+CRDT decision).
- **Control plane alpha for MSPs**: quotas, billing stub, upgrade flows
  including the first one-click staging clone; one-command k3s installer
  published.
- Community gauge → first talks; first grant applications.

### 2028–2029 — horizon

- **SaaS GA** on Scaleway; first paying SMEs.
- **MSP program**: onboarding, certification, support contracts.
- **Messaging** (Teams alternative) — the second wedge.
- **Kamara sync clients** (desktop, mobile); **E2EE** → federated encrypted
  replicas between trusting instances.
- **Importers** from the incumbent stack (SharePoint, OneDrive, AD):
  *planned, deliberately unscoped.* Built case-by-case from real customer
  conversations at MSP alpha — migration is the adoption bottleneck, but the
  devil is in the details, so we don't guess the details in advance.
- Office/document editing: exploration only, probably via integration first.
- North star check-in: if the suite holds, widen the catalog — the platform
  play begins.

### Success and kill criteria

Phase 0's exit numbers (active demo tenants, organizations self-hosting
monthly) are deliberately set at public-demo launch, not invented before there is
anything to measure. One criterion is fixed now, and it has teeth: **if 12
months after the public demo no external organization is self-hosting
Peristera, the thesis gets rethought before anything more is built.**

## 6. Open decisions (ADR backlog)

| # | Decision | Notes |
|---|---|---|
| 1 | Federation protocol design | Signed HTTP between instances; evaluate & likely reject ActivityPub/Matrix with reasons |
| 2 | CRDT library for Ergonomos | Yjs vs. Loro vs. Automerge — decide when multi-user starts, not before |
| 3 | Kamara encryption stance | E2EE vs. encryption-at-rest; E2EE deferred but the chunk format shouldn't preclude it |
| 4 | Control-plane openness | **Tentative decision — see §7.** Open (AGPL) in the monorepo; commercial add-ons later in a private repo |
| 5 | Object storage for Kamara chunks | S3-compatible (Scaleway/MinIO) vs. filesystem |
| 6 | Billing provider | When the control plane billing stub becomes real |
| 7 | Name of the messaging product | Phase 2 |
| 8 | Desktop sync client technology | Go + native shell vs. Flutter desktop |
| 9 | GDPR erasure semantics in hard cases | CRDT history compaction, erasure propagation across federation, dropping E2EE replicas, append-only audit logs (pseudonymized subjects + erasable mapping?) |
| 10 | OpenFGA modeling details | Shared model conventions across apps, federated subjects (instance-namespaced IDs), permission-filtered search and `ListObjects` cost |
| 11 | Importer scope and priority | Case-by-case, driven by customer conversations at MSP alpha — marked planned, not designed |
| 12 | Staging-clone data handling | A clone carries prod's personal data: full copy vs. masking, retention/erasure semantics in staging |
| 13 | Performance budget numbers & tooling | Concrete per-interaction budgets and the CI perf-check setup (acknowledged skill gap — revisit regularly) |
| 14 | Federation protocol threat model | Malicious/compromised peer instances, identity assertion validation — part of the protocol ADR. v1 trust model already decided: allowlist-only |

## 7. Licensing & governance

- **Applications: AGPL-3.0-or-later** with the App Store distribution
  exception (`LICENSE-EXCEPTION.md`). **Libraries: MIT.**
- **CLA** (`CLA.md`): contributors keep copyright, grant relicensing rights.
  This is what keeps dual-licensing/open-core optionality — stated openly.
  Known tension, accepted consciously: copyleft-plus-relicensing-CLA is the
  pattern that makes some self-hosters and contributors decline to
  participate. We answer it with transparency, not by pretending it isn't
  there.
- **Control plane (tentative — the one strategy point still under
  discussion):** the instinct to keep it private is commercially
  understandable, but it collides with Phase 0: the self-hosting community
  will not champion a stack whose most important component is a hidden
  binary, and "sovereignty, but trust us" doesn't survive contact with that
  audience. AGPL already prevents a competitor from silently productizing it,
  and the CLA preserves the right to add closed commercial modules (billing
  integrations, fleet management, white-labeling) in a separate private repo
  in years 2–3 — when they exist and are worth protecting. **Recommendation
  in force: control plane is open, AGPL, in the monorepo.** Revisit at MSP
  alpha.
- **Legal files are templates.** `CLA.md`, `CONTRIBUTING.md`,
  `CONTRIBUTORS.md`, `LICENSE-EXCEPTION.md` currently carry the "ergonomos"
  name; at M0 they move to `templates/legal/` and get instantiated per
  project subfolder.
- Maintainer: Jean-Luc Spielmann ([@jlspielmann](https://github.com/jlspielmann)).
  Governance is benevolent-dictator until there's a community to govern.

## 8. Repository layout & how to work here

**One monorepo** (decided): simpler solo, and one coherent context for LLM
sessions. Split a component out only if a real community forms around it.

```text
peristera/                  ← monorepo root (this file)
├── README.md               ← strategy + context. Read first, always.
├── Q&A.md                  ← archived decision interviews (provenance)
├── adr/                    ← ecosystem-level ADRs
├── guidelines/             ← UI, UX, marketing, development guides
├── templates/legal/        ← CLA, CONTRIBUTING, … instantiated per project
├── lib/                    ← shared Go libraries (MIT)
├── control-plane/          ← tenant lifecycle, catalog, quotas, billing
├── iam/                    ← Peristera IAM (layer over Zitadel)
├── ergonomos/              ← tasks → collaboration engine
└── kamara/                 ← file storage & sync
```

Each project folder gets its own `README.md`, `adr/`, and instantiated legal
files. Documentation sites are generated from these Markdown files.

### Working agreements (humans and LLM agents alike)

1. **Session start ritual:** read this README, then the target project's
   README and ADRs, then relevant `guidelines/`. Don't start coding on chat
   memory alone.
2. **Development loop:** specify → red → green → refactor. Behavior is
   specified as Gherkin `.feature` files at domain/API level; make it fail;
   implement the smallest change to pass.
3. **After meaningful work:** append to the project's `docs/worklog.md`; write
   an ADR for any non-obvious decision; extract reusable conventions into
   `guidelines/`.
4. **Keep changes small and CI green.** Every milestone stays demoable.
5. **Migrations are expand/contract (zero-downtime) from the very first
   migration.** The control plane performs upgrades *for* customers —
   rollback-ability and the staging-clone flow are product features, so
   migration discipline is not optional hygiene.
6. **This README is load-bearing.** When strategy or architecture changes,
   change it here in the same commit — a stale strategy document is worse
   than none, especially when an LLM treats it as ground truth.
7. **When planning new work, read the open GitHub issues first.** Reviews
   file deferred findings as issues (labelled by area/milestone); before
   scoping a milestone or session, check whether any issue touches the
   code you're about to open and fold it in — deferred debt is paid when
   you're already in that file, not in a separate pass that never comes.

## 9. Team & constraints

Solo founder (Jean-Luc Spielmann, Luxembourg), nights and weekends,
bootstrapped, seeking funding/grants once there is something public to show.
Development is primarily LLM-assisted — which is why this repository invests
unusually heavily in written context (README, ADRs, guidelines): the
documentation *is* the senior engineer in the room.

## 10. Risks

Named, so they can be watched rather than discovered.

| Risk | Mitigation |
|---|---|
| **Solo bus factor** | Everything is written down — this repository *is* the handover. |
| **Zitadel all-in, limited fallback** (Ory/Keycloak are worse fits) | Accepted consciously (see build vs. buy). Our own IAM API surface keeps the seam visible, even without a full abstraction layer. |
| **Federation cold start** (network effects) | Personal↔employer federation needs no second company; every MSP is a network seed (see §4). |
| **OnlyOffice dependency** (licensing/pricing changes, integration quality) | Bootstrap status, not marriage: the integration sits behind a document-service interface; building our own stays the documented last resort. |
| **Multi-year nights-and-weekends energy** | Milestones sized ≤ ~6 weekends, everything demoable, build in public — external feedback as fuel. |
| **k8s-only alienates part of the Phase 0 audience** | One-command k3s installer: one VM is enough. |
| **Scope creep by conviction** (this document keeps growing) | The sizing rule in §5 and the kill criterion above apply to the author too. |
