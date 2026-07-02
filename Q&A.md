Market & positioning**
1. Who exactly is the target SME? My guess: 10–100 employees, too small for a real IT department, currently on Microsoft 365. Which geography first — Luxembourg/Benelux, then DACH?

I want to market online - first in the self hosting comunity to get feedback, then go wider.

2. What's the *wedge* — the first Microsoft workload you realistically replace? Full M365 displacement is a 5+ year fight; SharePoint/OneDrive replacement (Ergonomos + Vault) while coexisting with Outlook/Teams seems the plausible entry. Agree?

Agree. I think that we need to build a teams alternative after that, and then focus on office.

3. Roadmap item 0 (admin dashboard: clients, billing, quotas) suggests you're building for **integrators/MSPs as the primary channel**, not selling to SMEs directly. Is that the strategy — integrators host and support, you provide software + maybe SaaS?

I want to make both. MSPs should have a keys in hand solution. But I also want to host this myself as a SaaS to get feedback, etc.

4. How do you position against Nextcloud, openDesk (ZenDiS), Seafile, OnlyOffice? Is the differentiator the Notion-like UX + opinionated defaults + genuine multi-tenancy for integrators?

a few angles:
  - opinionated defaults, no option deluge
  - no data silos - work with different companies, etc. directly via federation.
  - make it easy for MSPs to sell it.

5. Business model: where does money come from in year 1–3? Options: hosted SaaS, paid support/certification for integrators, dual-license enterprise features. The CLA hints at open-core/dual-license — confirm or deny.

year 1 is building and showcasing, building support for the cause. I want to apply for public grants. year 2-3 I want to sell as SaaS and start approaching targeting MSPs directly, especially with certifications, etc.

**Product & roadmap**
6. "Federation from the beginning" — via what, concretely? OIDC trust between instances, ActivityPub, Matrix, something custom? And federation of *what* first (identity, documents, chat)?

identity, documentation, files, etc. Ergonomos for example will be able to act as a todo list / project planner. This will create calendar entries. I want to see my private entries and my corporate entries in the same calendar, without needing to do something complicated. This will be the heart of the project - and I think this will allow me to get EU grants.

7. Peristera IAM: build on an existing engine (Keycloak, Zitadel, Ory) or from scratch? From scratch is a multi-year detour; I'd default to wrapping an existing one.

This is a good idea - but it makes me wonder if I should pivot then. Why do you think it is a multiyear project? If we decide into not building this ourselves, I think we should think about how we host this. Because if we have a thirdparty application in the stack, it becomes alot harder to control the experience. BUT, all clients will want to host their own applications at some point. Can we maybe do this differently? Make a realy slick UI to spin up pods in kubernetes with a click. That would be useful anyway, and allow from the start to something where I want to pivot later: Replace azure. Do you see where I am going with this?

8. Why is the admin/billing dashboard *before* IAM in the roadmap? Everything needs auth — I'd have expected IAM (or at least a stub) first. Is item 0 already underway?

I guess this should be build together. Auth as a stub, then admin dashboard as a stub. I want to have something useful as quickly as possible. Actually, I think all the tools should be build together: why? They all inform each other and I want to have basic but minimal functionality. This way, I can be opinionated, but pivot, if my opinions suck, without loosing too much time.

9. What are your actual time expectations — what should exist in 6 months, and what does "first paying customer/design partner" look like?

6 Mpnths: login via Peristera IAM, task management via ergonomos, file upload via vault on the browser. 

10. Vault: own sync engine, or build on something proven (e.g. Seafile core, rclone-style, or Nextcloud's)? Desktop + mobile clients in scope for the 2–3 year horizon?

own sync engine. I have been building a encrypted sync engine but have come to the conclusion that encrypting everything is a hassle in corporate world. But we can decide on that later.

**Tech stack (this is the most important part for LLM-assisted coding)**
11. What languages/frameworks have you chosen or do you lean toward — backend, frontend, database?

backends: Go
frontend: everything that lives on the web only: HTMX, maybe with some svelte or similar if the interaction model needs it. If the application lives also on device: flutter.
database: psql

13.  If undecided, tell me your constraints (e.g. "must be boring, hireable-for in Luxembourg, good LLM training coverage") and I'll write it as decision criteria instead.

Do you agree with my stack? Is this well represented in the LLM training data?

12. Deployment target for self-hosters: single Docker Compose on one VM? Kubernetes for integrators? This shapes architecture heavily.

I think we should go with Kubernetes only and provide docker images without much documentation except how to run them. This will be a heavy framework, with a lot of moving parts. If we want to provide a safe experience, we need a "controlled" environment. Do you agree?

13. Multi-tenancy: one instance per customer, or shared instance with tenant isolation? Item 0 (quotas, billing) implies shared.

The admin dashboard should be like a control pane. New customer? Let's spin up a new namespace, and launch the pods. This way, we can cleanly seperate databases, etc.

**Team & constraints**
14. Are you solo, and is this full-time or nights-and-weekends? Bootstrapped or seeking funding? The 2–3 year roadmap needs to be sized honestly against this.

I a solo, bootstrapped but seeking funding. Nights & weekends.

15. Naming: should the legal files be generalized from "ergonomos" to "Peristera", and is "Peristera Ergonomos / Peristera Vault / Peristera IAM" the final naming scheme?

The files should be templates and adapted for each subfolder.

---

# Round 2 (2026-07-02) — Claude's answers to your questions, then follow-ups

## Answers to your questions

**Q7 — why IAM from scratch is a multi-year detour.**
The OIDC/OAuth2 spec surface alone is large (auth-code + PKCE, token lifecycle,
JWKS rotation, discovery, introspection, revocation, sessions), and the
table-stakes features on top are each a project of their own: MFA and passkeys,
SCIM provisioning, LDAP / Entra ID import (SMEs leaving Microsoft *will* arrive
with Active Directory — you need that migration path), brute-force protection,
account recovery, audit logging, consent screens. And unlike other components,
every bug here is a security incident; MSPs will also ask about OpenID
conformance certification. Keycloak has ~10 years of hardening you can't
compress on nights and weekends.
**Recommendation:** wrap **Zitadel** — written in Go (you can read and patch
it), API-first, multi-tenant by design, OIDC-certified, runs well on
Kubernetes. Build the Peristera IAM UI against its APIs so the *experience*
stays yours. Ory (Kratos/Hydra) is the fallback; Keycloak is the most
battle-tested but Java + config deluge is the opposite of "opinionated
defaults". First task: a 2-week evaluation spike, written up as an ADR.

**Q7b — the pivot ("spin up pods with a click … replace Azure"). Yes, I see it:**
the control plane is not internal tooling, it **is** the MSP product — and it
dissolves your third-party worry. You control the experience by controlling
deployment, configuration, upgrades and backups of every component, not by
owning every line of code. Wrapping Zitadel stops being a compromise and
becomes the first entry in your app catalog.
Two cautions: (1) a *generic* Azure replacement is a harder fight than M365 —
Rancher, Portainer, Cozystack, Plural already live there, and your edge is the
integrated federated suite, not a nicer Kubernetes UI. (2) Keep the catalog
curated for the first 2 years (Peristera apps + IAM engine + Postgres
operator), not arbitrary workloads. "Replace Azure" belongs in the README as
the long-horizon north star, not a year-1 promise.

**Q11 — stack verdict and LLM training coverage.** Agree, with caveats:
- **Go** — ideal. Best-possible LLM coverage; the whole k8s ecosystem is Go;
  single-binary deploys.
- **Postgres** — yes. On k8s use the CloudNativePG operator; one Postgres per
  tenant namespace matches your isolation model.
- **HTMX** — right for the control plane, dashboards and IAM screens. It will
  **not** carry the Notion-like Ergonomos editor: block-based collaborative
  editing needs a real client framework plus CRDTs. Plan Svelte islands +
  Yjs (or Loro) for that one surface from the start, rather than discovering
  it mid-build.
- **Flutter** — fine for mobile. Flutter Web is weak, so keep browser surfaces
  HTMX/Svelte. The Vault desktop sync client may end up better as Go + native
  shell; decide later.
- **LLM coverage** — Go, HTMX, Svelte, Flutter, Postgres are all richly
  represented. Thin spots: CRDT internals and k8s operators/controllers.
  Mitigation is exactly this exercise: ADRs + guidelines the LLM reads first.

**Q12 — Kubernetes only?** Agree on architecture: one deployment contract (the
k8s API), no compose-vs-k8s support matrix, a controlled environment. But it
collides with your go-to-market — the self-hosting community you want feedback
from first mostly runs a single VM and bounces off "k8s required".
**Resolution:** Kubernetes is the only *contract*, and you ship a one-command
**k3s installer** so a single Hetzner-class VM is enough. You keep the
controlled environment; hobbyists keep their one box. Two documented paths
only: "one VM via our installer" and "bring your own cluster".

## Follow-up questions

### A. Strategy

**R1. Suite-first or platform-first?** This decides the first sentence of the
README. My recommendation: **suite-first messaging, platform-first
architecture** — "the federated M365 alternative for European SMEs" is sellable
today, "European Azure" is not yet credible from a solo project, but every
architectural decision (control plane, namespaces, catalog) is made as if the
platform is the endgame. Agree, or do you want the platform to lead?

> Agree

**R2. Grants.** Which programs have you actually looked at? Worth checking:
NLnet / NGI Zero (funds exactly this kind of federation/sovereignty work,
~€5–50k, milestone-based, solo-friendly), Luxinnovation / Fit4Start
(Luxembourg), Sovereign Tech Agency (DE, more for maintenance of existing
critical software). Should I shape the roadmap so that a self-contained,
fundable work package (e.g. "cross-instance federation protocol + reference
implementation") can be cut out and submitted as-is?

> I think we should worry about this when we have something to show.

**R3. Year-1 showcase, concretely.** Public demo instance? Build-in-public
blog? FOSDEM/froscon talk? And where will the SaaS run — the sovereignty story
implies an EU provider (Hetzner / OVH / Scaleway)?

> public demo instance, we build in public, running on scaleway. Talks are a good idea, but I need to gauge community feedback. For that I need to have something to show.

### B. Product

**R4. Federation mechanics — the heart, so let's pin it down.** Two separate
questions hiding in your answer:
(a) Peristera↔Peristera only at first, or must v1 interop with non-Peristera
systems (Nextcloud, Google/Microsoft calendars)?
(b) In your merged-calendar example, is "my private entries" a *personal
Peristera instance*, or your existing Gmail/iCloud calendar? If the latter,
that's CalDAV/ICS client work, not federation, and much cheaper.
My proposal for the README: identity federation via OIDC between instances;
calendar via CalDAV/ICS (interops with everything immediately); document/task
sharing via a small signed HTTP protocol between Peristera instances, designed
in an early ADR with ActivityPub considered and probably rejected. OK?

> I want to have peristera-peristera federation only. Everything else is too involved. We can build adapters for 3rd party applications that make it possible to ingest caldav for example. This way, I don't need to ditch my personal google calendar, but get the benefits from federation. 

**R5. Ergonomos at 6 months:** single-user task lists (HTMX, cheap), or already
collaborative/real-time (pulls the whole Svelte+CRDT stack forward)? I'd argue
single-user-per-object with live-refresh is enough for the 6-month demo.

> single user, but minimal experience. It is more important to have a multi user working then having all the bells and whistles on single user.

**R6. Your existing encrypted sync engine:** what language is it in, is it
public, and does Vault grow from that codebase or restart clean? (E2EE vs
encryption-at-rest stays an open ADR either way.)

> it is public, written in go. We will restart clean, but get inspired. But I want to start with chunking, uploading, etc, and get a good experience. I also want to build a good API ontop, so that other applications in the stack can use it as simple storage layer. E2EE comes after that. It will allow to federate storage. (I can store my friends data as a second copy, and he mine, but we can't see what's in the data.)

**R7. IAM:** OK to write "Peristera IAM = branded layer over Zitadel (evaluate
vs Ory, 2-week spike, ADR-first)" into the roadmap?

> Yes, Peristera over Zitadel.

### C. Repo, licensing, next step

**R8. Monorepo or multi-repo?** This folder isn't a git repo yet. One monorepo
(subfolder per project) is simpler solo and much better LLM context; separate
repos fit the per-subfolder legal templates and per-project communities later.
My recommendation: monorepo now, split later if a community forms around one
component. Which?

> monorepo

**R9. License of the control plane itself.** Apps AGPL, libraries MIT — and
the control plane? It's the commercially interesting piece for MSPs. Fully
AGPL too, or is this where open-core / paid features live in year 2–3? The
README should say this honestly, whichever it is.

> I think the control plane should be hidden for now. So that means seperate repo ... Not ideal, what do you think?

**R10. First artifact after the README.** "Everything as stubs together"
still needs a first commit. Pick one: (a) control plane skeleton that can
create a tenant namespace and deploy one pod, (b) the Zitadel evaluation
spike, (c) Ergonomos task-list stub. My vote: (b) then (a) — auth is the
dependency of everything, and the spike is time-boxed.

> b, then a.

---

# Round 3 (2026-07-02) — closing the cold review's gaps: risks, metrics, pricing, community

The uncontexted review found things a strategy doc should have that ours
doesn't. Answers here become new README sections.

**R11. Risk register.** I'll draft a "Risks" section with these, each with a
mitigation — correct, extend, or reweight:
- solo bus factor (mitigation: everything written down — this repo *is* the handover)
- Zitadel all-in with limited fallback (mitigation: our IAM API surface keeps the seam visible, even without a full abstraction layer)
- federation cold start (mitigations now in README: personal↔employer federation, MSP-as-network-seed)
- OnlyOffice dependency (licensing/pricing changes, integration quality)
- multi-year nights-and-weekends energy/motivation risk
- k8s-only alienating part of the Phase 0 feedback audience (mitigation: k3s one-command installer)

>ok

**R12. Success and kill criteria per phase.** What measurable signal ends
Phase 0 and justifies building the paid SaaS (e.g. "N organizations
self-hosting monthly", "N demo tenants active after 30 days")? And is there
an honest kill/pivot criterion — e.g. "if 12 months after the public demo
nobody self-hosts it, rethink the thesis"? Your numbers, even rough.

>12 months is good

**R13. Pricing straw man.** "The MSP's margin is our go-to-market" needs one
concrete number to be credible. E.g.: SaaS at €X/user/month; MSPs pay €Y
wholesale (or flat per-tenant) and set their own retail. What X and Y feel
right as a first hypothesis? (For reference: M365 Business Standard is
~€12–13/user/month.)

>no per user pricing. SaaS is usage based with a flat admin fee per month (15€?) and MSPs pay for certification and priority support, based on number of tenants.

**R14. Contact & community front door.** Build-in-public needs an address:
GitHub org name (is `peristera` free?), issues on GitHub — plus which
channel: Matrix room (on-brand for federation), Discourse, Mastodon/Fediverse
account, plain email? Pick the smallest set you'll actually tend.

>peristera-io is the github org name. email is peristera@peristera.io. 

**R15. BDD toolchain.** CONTRIBUTING mandates Gherkin `.feature` files; for
Go the standard is **godog** (the official Cucumber implementation). OK to
fix godog in the stack table, or another preference?

> ok

**R16. Federation trust model, v1.** Allowlist-only federation (instances
explicitly trust each other — arranged by the MSP or by admins exchanging
keys) vs. open discovery like email/Matrix? My strong recommendation:
**allowlist-first** — it kills the malicious-peer problem for v1, matches the
MSP topology, and open discovery can come later. Agree?

>ok

**R17. The encrypted sync engine.** Name + link to the public repo, so the
README's Kamara row can reference it (the reviewer flagged it as
unverifiable).

>ignore it for now.

---

# Round 4 (2026-07-02) — M1 planning: the Zitadel spike

Plan draft in `docs/m1-plan.md`; these decide its open parameters. Each has a
recommendation — confirm or push back.

**R18. Where does the M1 dev cluster live?** ADR-0003 fixes local dev = k3s
from M2, but M1 needs a cluster now and you're on a Mac (k3s is Linux-only).
Options: (a) **k3d** — k3s in Docker, closest to the real contract, needs a
container runtime on the Mac (OrbStack/colima/Docker Desktop); (b) a cheap
Hetzner/Scaleway VM running real k3s. My recommendation: **k3d locally** —
fastest inner loop, free, and it front-runs the M2 local-dev setup. Which,
and what container runtime do you already run on the Mac?

> docker is on this system; lets start with that. I will also prep a local cluster with 3 PCs for some real world tests.

**R19. Topology prior: Zitadel per tenant namespace.** The spike decides
per-tenant vs. shared-with-orgs *with footprint numbers*, but I want the
prior on record: per-tenant matches the isolation model and makes every
tenant its own OIDC issuer (which federation later rides on). The honest
counter-scenario: if Zitadel + login app + Postgres idle at, say, >1 GB per
tenant, an MSP's twenty-tenant single VM is dead and shared-with-orgs must
win despite being uglier for off-boarding and erasure. OK to treat
per-tenant as the leaning to disconfirm?

> Let's start with virtual instances per tenant and see how that feels. Zitadel requires 512MB RAM per instance. This will be too heavy, if I have 10 instances running, but only 40 users. We should however thing about the possibility to break out a tenant to have their own instance, for example if they require too much resources, or if they have legal requirements. Agree?

**Outcome (follow-up discussion, same day).** Clarified: 512 MB is per
*deployment*, not per virtual instance — virtual instances are logical and
the marginal tenant is ~free, so one shared deployment carries the ten-tenant
VM easily. Settled: **shared Zitadel deployment in a platform namespace, one
virtual instance per tenant, break-out to a dedicated instance kept possible**
(via `zitadel mirror --instance` + re-pointing the tenant's domain; also
available as a provisioning-time flag for legally-demanding tenants). Three
day-one rules make break-out cheap: domain per tenant from the first tenant,
per-tenant IAM endpoint config in apps/control plane, break-out as flag not
only migration. Details in `docs/m1-plan.md`; ADR-0006 confirms with spike
evidence.

**R20. Login experience path.** Zitadel's current login (Login v2) is a
self-hostable Next.js app — brandable, but it puts a **Node runtime** into
the per-tenant catalog next to our all-Go stack. Alternative: build our own
login UI on Zitadel's Session API — full experience control, but we then
own security-sensitive screens (password, MFA, recovery), which is exactly
what ADR-0004 said we don't want to own. Recommendation: **Login v2 with
branding for M1–M6**, own-UI stays the documented escape hatch if branding
proves too limited; the spike probes those limits. Swallow the Node runtime?

> I realy don't want a node runtime. But I don't think that building the UI for this, which is just alot of work without validating the overall model is very helpful. So, let's go with node, get experience and see if we  want to change this later.

**R21. Spike code: keep or throw away?** Recommendation: **keep** — the stub
relying party becomes the seed of `iam/` (README, legal files, first Go
code), and its manifests/API calls are M2's raw material. Throwaway code
would violate "everything ships as a vertical slice" for no gain. OK?

> ok

**R22. CloudNativePG already in M1?** The operator is formally M2 territory,
but Zitadel-on-CNPG is precisely the integration risk worth confirming, and
installing the operator on k3d is cheap. Recommendation: **yes, CNPG from
session 1** — a plain Postgres container would confirm nothing about our
actual target. OK?

> ok.
