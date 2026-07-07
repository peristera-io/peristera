# Pre-M7 hardening plan (Tier 1 + Tier 2)

Before M7 puts the control plane and tenants on the public internet, close the
security and reliability gaps that a public, multi-tenant deployment makes
real. Scope = the triage Tier 1 + Tier 2 (2026-07-07). Parameters in `Q&A.md`
Round 12.

## Tier 1 — gates a public demo

| Issue | Gap | Shape |
|---|---|---|
| **#1** | Control-plane operator authZ: any principal who can authenticate to the default instance is a full operator (no audience/role/subject check). | **Design decision + ADR** (Q&A R12). Validate token audience + an operator authorization model. |
| **#7** | Control-plane SA has `secrets: list/watch` cluster-wide (can read the Zitadel masterkey, system-user key). | Mechanical: per-namespace Roles / a selector so the SA can't read platform secrets. |
| **#6** | Initial-admin credentials are permanent (never rotated / no forced change). | Force-change-on-first-login and/or a rotate path (Q&A R12). |
| **#38** | Kamara browser pages send no Content-Security-Policy. | Mechanical: add a CSP header to the cookie UI responses. |
| **#48** | Office engine provisioning is dev-shaped (SYS_ADMIN, admin/admin, no TLS) with no env gate. | Only if office is in the M7 demo (Q&A R12); else stays deferred. |

## Tier 2 — reliability for a real deployment

| Issue | Gap | Shape |
|---|---|---|
| **#30** | Blob-backed Kamara Deployment uses the default rolling update → two pods can multi-attach the RWO blob PVC on a deploy. | Mechanical: `Recreate` strategy + pinned `replicas: 1` for stateful apps. |
| **#31** | Tenant reports `Ready` while an app pod/PVC is still stuck. | Mechanical: gate `Ready` on app workload/PVC readiness. |
| **#26** | Kamara bearer-token cache honours a revoked/expired token for ≤ its TTL. | Tighten (shorten TTL vs. validate JWT `exp`/introspect — Q&A R12). |

## Approach

- **#1 is the anchor** and needs its own ADR (the operator authorization model,
  which README §4 says should probably go through OpenFGA like everything else).
  Draft the ADR after Round 12; it also naturally tightens the audience gap and
  informs #26 (validate the token properly, not just "is it live").
- The mechanical fixes (#7, #38, #30, #31) need no decision and proceed in
  parallel with the Round-12 answers.
- Deliver as a short sequence of focused commits (one issue or a tight cluster
  per commit), each verified in-cluster where it touches provisioning; a
  fresh-context review before merge, as with M5/M6.

## Out of scope (stays deferred)

Tier 3 (sharing/erasure correctness — not exercised by a single-tenant demo,
issues #33/#35/#34/#22/#20/#23/#24/#17), Tier 4 (#43 accepted, features, testing,
tech-debt). #28's Content-Disposition/RFC-6266 half already shipped in M6; only
its error-surface-docs remainder is left.
