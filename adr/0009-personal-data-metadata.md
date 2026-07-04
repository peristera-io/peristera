# ADR-0009: Personal-data metadata — the GDPR-by-design contract

- **Status:** accepted
- **Date:** 2026-07-04
- **Provenance:** README §3.8, §4 "GDPR by design"; M0 deferral ("before
  M3/M4 store their first byte"); Q&A Round 6 (R29); `docs/m3-plan.md`.
  Homes the previously-unattached crypto-shredding convention (issue #9).

## Context

Ergonomos (M3) is the first app to store personal data. GDPR is a design
constraint, not a feature (Principle 8): export, erasure, retention, and
the Article 30 processing registry must be first-class from the first
byte, or they are never retrofittable. This ADR fixes the convention every
Peristera app follows; it is a `lib/pii` package, not Ergonomos-shaped.

## Decision

1. **Every entity that can hold personal data declares it at the model
   level.** A Go type registers a descriptor with `lib/pii`: which fields
   are personal data, how to resolve the **data subject(s)** the record
   relates to, its **retention class**, and legal-hold applicability.
   Struct tags express the field-level marks; a small registry holds the
   per-type resolver/retention so tooling acts generically.
2. **Data subjects are instance-namespaced IDs** so the contract is
   federation-ready: a subject may live on another instance. **Canonical
   form (pinned here, used everywhere — it is persisted and permanent):**
   `<home-instance-domain>/<user-id>` (e.g. `demo.127.0.0.1.sslip.io/2789…`),
   where the domain is the tenant's permanent issuer domain (ADR-0006 §2).
   In OpenFGA this subject is the object `user:<home-instance-domain>/<user-id>`
   (ADR-0010); in audit and search it is referenced indirectly (see §7 and
   ADR-0011 §4). This is the ADR-0007 §3 federated-reference shape
   specialized to `type = user`. Every personal-data record must answer
   "which person(s)?" — for a task, its owner and any person referenced in
   content that the user marks.
3. **Export and erasure are per-app hooks, orchestrated later.** `lib/pii`
   defines the interface each app implements: `ExportSubject(subjectID)`
   (machine-readable) and `EraseSubject(subjectID)` (cascade following the
   same metadata). If export can find it, erasure can remove it — except
   the append-only audit log (ADR-0011), handled by pseudonymization.
   The whole-tenant orchestration across apps (control plane) is a later
   milestone; the per-app hooks land now.
   **Erasure ordering (correctness rule):** authoritative source rows are
   erased *before* any derived store is dropped/rebuilt (ADR-0012's search
   index), so an in-flight rebuild cannot re-materialize erased data. The
   append-only audit log is neither deleted nor rebuilt — it is
   pseudonymized (ADR-0011 §4).
4. **Retention classes are the erasure mirror.** A class carries a
   duration and legal basis (e.g. `none`; `accounting-10y-lu`; `contract`;
   `employment`). Erasure refuses or defers what a class or an active
   **legal hold** requires keeping. Without this, customers either break
   retention law or never dare use erasure.
5. **Processing registry falls out of the registry** (Article 30): the set
   of registered descriptors *is* "what personal data is stored where."
6. **Crypto-shredding / per-tenant key hierarchy (GH issue #9 home).**
   Whole-tenant erasure from immutable backups reduces to deleting the
   *tenant's* key (README §4). This ADR *specifies* the per-tenant key
   hierarchy as that mechanism; it is **implemented with the backup story
   (target M6)**, not in M3. **Scope is deliberately whole-tenant only** —
   it does not erase a single subject, and it is *not* the mechanism
   audit-log pseudonymization uses (that needs a per-subject secret, a
   distinct thing; see §7 and ADR-0011 §4). Recording it here gives the
   convention the milestone home it lacked.
7. **Per-subject pseudonymization (the audit-log mirror).** A single
   subject's erasure cannot delete append-only audit rows, so those rows
   reference the subject not by its ID but by a **per-subject pseudonym
   token**; a small `subject_pseudonyms` mapping (token → canonical
   subject ID, §2) resolves it. Erasing a subject deletes its mapping row,
   breaking linkability while the tamper-evident event chain stays intact.
   This is per-subject and separate from §6's per-tenant key. `lib/pii`
   owns the token allocation so audit and any other indirect referrer
   share one pseudonym per subject.

**M3 scope (R29):** implement the annotation + registry + export/erase
hooks in `lib/pii`, exercised by the Ergonomos task entity; the
per-subject pseudonym allocation + `subject_pseudonyms` mapping (§7) —
because the token must be in the audit schema from day one (it can't be
retrofitted into an append-only table). Retention classes: the mechanism +
the `none` default. Whole-tenant orchestration, the per-tenant key
hierarchy (§6), and content-level subject tagging beyond the owner are
deferred (design-ready, not built). `EraseSubject`'s cascade is thin at
single-user/`none`-retention scale — the interface is the unretrofittable
part; trim the implementation depth first if session 3 overruns.

## Consequences

- A new data model that can't answer "which person, how to export, how to
  delete, how long to keep" does not ship — enforced by requiring a
  `lib/pii` descriptor for any table flagged personal-data.
- Testable: a godog scenario can assert export returns a subject's task
  and erasure removes it.
- Some fields (audit subjects) are personal data that must be *kept*
  tamper-evident — the tension ADR-0011 resolves via pseudonymization.

## Alternatives considered

- **Bolt export/erasure on later as batch scripts** — the exact
  deadline-driven failure Principle 8 exists to prevent. Rejected.
- **Field tags only, no resolver registry** — can't answer "which person"
  generically or cascade erasure. Rejected.
- **Encrypt-everything for erasure** — the corporate-usability problem the
  founder already rejected for Kamara (Q&A R6/Round 2); crypto-shredding is
  scoped to whole-tenant/backup erasure, not per-field.
