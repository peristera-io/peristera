# Milestone stub — service-to-service auth / intra-tenant zero-trust

> **Superseded (2026-07-06).** This stub is scheduled as **M5** and its
> parameters are settled in `Q&A.md` Round 10. The live plan is
> **`docs/m5-plan.md`**. This file is retained only for the original
> framing; read the plan and Round 10 for current decisions.

- **Status:** superseded by `docs/m5-plan.md` (was: proposed, Q&A R41,
  2026-07-05). Scheduled as M5 (Q&A R49); runs before OnlyOffice (M6) and
  the public demo (M7).
- **Lifecycle:** this is a placeholder that captured *why* the milestone
  exists and its open questions; now superseded by the milestone's plan
  (`docs/m5-plan.md`) and Round 10.

## Why this exists

Kamara (M4) surfaced the first real app-to-app call: Ergonomos attaching a
file by calling Kamara's storage API. Wiring it forced a choice about **how
one Peristera app authenticates to another** — and that choice is not a
Kamara detail. It is the platform-wide **service-to-service (S2S) contract**
every future app-to-app interaction inherits. Settling it expediently (to
make one acceptance test pass) would bake in a trust model we'd have to
unpick later, so M4 deliberately deferred it (M4a acceptance became a
same-app API round-trip; the cross-app file-attach moved to M4b via a
browser-mediated, no-S2S-trust flow — "option C").

## The decision space (from Q&A R41)

- **(A) Forward the user's access token.** App A sends the logged-in user's
  token to app B; B validates it and treats the user as the actor. Cheapest,
  but assumes **mutual trust between co-located apps** — B cannot tell *which*
  service is calling, only that some valid tenant user token was presented.
  This is the opposite of zero-trust.
- **(B) Machine identity + on-behalf-of.** Each app has its own service
  credentials; it obtains a token via client-credentials and/or **OAuth2
  token exchange (RFC 8693)** that names both **actor** (the calling
  service) and **subject** (the user). Proper machine identity, actor-aware
  audit, decoupled from user-token lifetime. This is what **zero-trust
  inside the tenant namespace** requires.
- **(C) No S2S call.** The user's browser talks to each app directly with
  its own session; apps exchange only *references* (e.g. a file-id), each
  authorizing its own user. No inter-service trust at all. Used for the M4b
  file-attach; not a general answer for server-initiated app-to-app work.

## Acceptance test (its real consumer)

The **Ergonomos file-attach flow** is this milestone's acceptance test
(moved here from M4b, Q&A R9): Ergonomos attaches a file to a task by
obtaining a credential and calling Kamara's storage API under whatever S2S
model this milestone chooses. Designing the model and proving it with a real
cross-app call happen together — the decision is validated by its first
consumer, not on paper.

## What this milestone must design

- The platform S2S authentication model (very likely **B**), as an ADR that
  binds all apps, not just Kamara ↔ Ergonomos.
- **Machine/service identity** per app (service accounts in the tenant
  Zitadel instance) and how they're provisioned by the control plane.
- **Token exchange / actor tokens** (RFC 8693) support and config in
  Zitadel; how audit records actor-vs-subject (ADR-0011 gains an actor).
- **NetworkPolicy** to default-deny lateral traffic in the tenant namespace
  (issue #18).
- **Per-service authorization**: OpenFGA authn on the OpenFGA endpoint and
  a service-caller dimension in the model (issues #18, #19).
- Migration path: how the M4b browser-mediated flows (option C) evolve to
  server-initiated B calls where genuinely needed.

## Folds in existing issues

- **#18** — NetworkPolicy + OpenFGA authn (was tagged M6).
- **#19** — OpenFGA model accretion (service-caller dimension).

## Non-goals

Not federation/cross-tenant trust (a separate, later concern). Not a
service mesh / mTLS sidecar mandate unless the design shows it's the
simplest way to machine identity — decide in the ADR, don't presume.
