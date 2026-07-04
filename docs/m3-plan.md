# M3 plan — Ergonomos stub + the suite's cross-cutting spine

- **Size:** the heaviest milestone so far, and it will likely exceed the
  ≤6-weekend rule if the conventions and the app both ship full. **Split
  proposed (see Round 6 Q1):** M3a = conventions (ADRs + `lib/`), M3b =
  the Ergonomos task stub that consumes them. Dates are targets.
- **Status:** parameters settled (`Q&A.md` Round 6, 2026-07-04). Starts
  after M2 (done 2026-07-04). **Split confirmed: M3a (conventions +
  `lib/`) then M3b (task stub).**
- **Settled (Round 6):** split M3a/M3b (R28); ADR all four conventions,
  implement only what the single-user stub exercises + search write-hook
  (R29); **database-per-app** inside the tenant CNPG cluster (R30);
  **keep the catalog a Go slice, grow the contract** — catalog-as-data
  deferred and recorded in its own ADR so the decision isn't lost (R31);
  **goose** for migrations, expand/contract from migration one (R32);
  **axe-core/pa11y** a11y checks in the e2e job, WCAG 2.1 AA (R33).
- **Lifecycle:** working document, superseded by the M3 ADRs and worklog.

## Why M3 is different

M0–M2 built the platform with **zero user data**. Ergonomos is the first
app that stores a byte a person typed — so it is the milestone where the
"GDPR is a design constraint, not a feature" principle (README §3.8) stops
being prose and becomes code. README §5 is explicit: personal-data
metadata, OpenFGA, audit events, and the search feed are **ADR'd before
M3/M4 store their first byte**. That is M3's opening cost, not M4's.

The trap to avoid: building four heavyweight subsystems for a single-user
todo list (violating KISS/YAGNI and the sizing rule). The line this plan
draws — **decide all four now (cheap to decide, ruinous to retrofit),
implement only what the single-user stub actually exercises** — is the
main thing Round 6 must confirm.

## What M3 attaches first (the conventions — M3a)

Each is an ADR *and* a `lib/` package (README §4: shared conventions live
in `lib/`, MIT). Ergonomos is their first consumer; Kamara (M4) the second
— so they must be reusable, not Ergonomos-shaped.

1. **Personal-data metadata** (README §4 "GDPR by design"). Schema-level
   annotation of which fields/objects relate to a natural person, carrying
   retention class and legal-hold flags. Drives export + erasure
   generically. *Implement:* the annotation mechanism + a per-app
   export/erase hook, exercised by the task entity. The whole-tenant
   orchestration (control plane) stays a later milestone.
2. **OpenFGA model conventions** (README §4). One shared authorization
   model; each app contributes relations. *Implement:* one OpenFGA
   instance per tenant namespace (ADR-0003 already says so — the control
   plane provisions it in M3), and Ergonomos'  `owner` relation on tasks.
   Single-user means the model is trivial today, but the *conventions*
   (federated-subject IDs, `ListObjects` for permission-filtered lists)
   must be right now.
3. **Audit events** (README §4). Every mutation emits a typed, per-tenant,
   append-only event. *Implement:* the emit path + storage; impossible to
   retrofit (old code paths never emit). The audit log is itself personal
   data — the pseudonymized-subject/erasable-mapping question (ADR backlog
   #9) gets at least a recorded decision.
4. **Search feed** (README §4). Per-tenant Postgres FTS index, results
   permission-filtered through OpenFGA. *Implement:* the write-side hook
   (mutations feed the index); the cross-app query UI can wait — but the
   ADR fixes the contract so later apps feed it too.

Plus two non-convention attachments due at M3:

- **Accessibility CI** (README §5 M0 deferral, "with M3 — the first real
  UI"). Automated a11y checks (axe-core/pa11y) against the Ergonomos UI;
  EN 301 549 / EAA as the bar.
- **`lib/` extraction of the OIDC relying-party + session code**
  (issue #2). Ergonomos is the third copy of the auth-code+PKCE+session
   flow (stub, control plane, now this) — the rule-of-three moment.
  Extract `lib/oidcrp` (+ session) first, refactor stub and control plane
  onto it, then Ergonomos consumes it. This also lands the session-store
  convention (its own ADR, promised before M3) and resolves issues #3
  (cache eviction) and the logout/version drifts noted with #2.

## The Ergonomos stub (M3b)

A catalog app in the tenant namespace, like the M1 stub but real:

- Single-user **task lists / projects** — create, edit, complete, delete,
  organize into lists. HTMX, server-rendered, "minimal but pleasant"
  (README §5). Multi-user, the Notion block editor, Svelte+CRDT, and
  emitted calendar entries are all **out** (2027 / ADR backlog #2).
- OIDC login against the **tenant's own** Zitadel instance (via `lib`).
- Its own database in the tenant's CNPG Postgres (see Round 6 Q3),
  expand/contract migrations from the first migration (agreement #5; tool
  choice in Round 6 Q5).
- Every task carries personal-data metadata, emits audit events, feeds the
  search index, and has an OpenFGA `owner` relation — i.e. it is the proof
  that the four conventions compose.
- Deployed by the control plane: the catalog contract grows to "an app
  can need a database + an OpenFGA store" (Round 6 Q4).

## Session schedule (indicative — sizing is at risk, see Q1)

| Phase | Session | Work |
|---|---|---|
| M3a | 1 | ADRs: personal-data metadata, OpenFGA conventions, audit events, search feed (write them first, together — they interlock) |
| M3a | 2 | `lib/` extraction: `oidcrp` + session (issue #2/#3), refactor stub + control plane onto it |
| M3a | 3 | `lib/` convention packages: personal-data metadata + audit + FTS feed skeletons, with unit tests |
| M3b | 4 | Control plane: OpenFGA per tenant, app-needs-a-database in the catalog contract; migration tooling |
| M3b | 5 | Ergonomos task stub — godog spec-first; tasks CRUD wired through all four conventions |
| M3b | 6 | HTMX UI + a11y CI; polish; worklog + README; demo recording |

**Abort valve:** if M3a overruns, ship the ADRs + the conventions Ergonomos
*actually touches* (metadata, audit, owner-relation) and defer the search
query UI — but never defer the ADRs; a missing convention decision is the
expensive kind of debt.

## Folded-in issues (agreement #7)

- **#2** shared `lib/` OIDC/session — M3a session 2 (core to the milestone).
- **#3** cache eviction — rides along with the session extraction.
- **#6** initial-admin credential lifecycle — control-plane/IAM work, due
  around M3; slot into session 4 if force-change's dependency (account
  recovery) is cheap, else keep as a tracked issue with a note here.
- **#9** crypto-shredding milestone home — the personal-data metadata ADR
  (session 1) is the natural place to finally attach it.

## Out of scope (deferred, not dropped)

- Multi-user, real-time, the Notion block editor, Svelte, CRDT (2027;
  ADR backlog #2 CRDT library).
- Emitted calendar entries + the merged-calendar/CalDAV story (2027).
- Whole-tenant export/erasure orchestration in the control plane (the
  per-app hooks land now; the orchestrator is later).
- Kamara / file storage (M4); OnlyOffice (M5); public demo (M6).
- Federation of tasks (2027) — but task object IDs are federation-ready
  (UUIDv7, ADR-0007) from the first row.
