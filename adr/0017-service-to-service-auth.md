# ADR-0017: Service-to-service auth (on-behalf-of via token exchange)

- **Status:** accepted
- **Date:** 2026-07-06
- **Provenance:** Q&A R41 (deferral) and R50/R51 (the model); `docs/m5-plan.md`
  (M5 sessions 2–4); the token layer of ADR-0016's three-layer model.
  Supersedes the stub `docs/s2s-auth-milestone.md`. Refs #29.

## Context

M4 surfaced the first real app-to-app call (Ergonomos attaching a file by
calling Kamara's storage API). *How* one Peristera service authenticates to
another is a platform-wide contract, not a Kamara detail, so M4 deferred it
(the browser-mediated attach, ADR-0016 "option C", needs no S2S trust).

ADR-0016 established that the network layer answers "may A reach B" and
OpenFGA answers "may user U touch this resource". This ADR owns the **token
layer**: "is this really service A, acting for user U?" — authentication and
the on-behalf-of assertion, not per-service authorization rules.

## Decision

**Model B — machine identity + on-behalf-of (R50/R51).** A service calls
another *as itself, carrying the user*, using **OAuth2 token exchange
(RFC 8693)**. We reject:

- **Forwarding the user's token** (A sends B the raw user token): B cannot
  tell which service is calling — the opposite of zero-trust.
- **Impersonation by user-id** (a service asks the IdP for a token for any
  user, no user token involved): a service could act as any of its users.
  We exchange the user's **real, present** access token instead.

### The concrete mechanism (Zitadel v4.15, verified)

The IdP is Zitadel; token exchange is on by default. The **caller's machine
identity is a confidential OIDC "S2S client"** — one per app that makes
calls — with:

- the **token-exchange grant** (`OIDC_GRANT_TYPE_TOKEN_EXCHANGE`),
- **`private_key_jwt`** client auth (the app holds a private key; Zitadel
  stores only the public key — no shared secret at rest, matching how the
  control plane already authenticates to Zitadel), and
- **JWT access tokens** (so the callee can validate locally — s3).

Non-obvious findings that shaped this (spike, commit `5fb01ac`):

- The S2S client **must be an OIDC app**, not a machine user or an API app.
  Zitadel's `invalid_client: no active client not found` is its (misleading)
  error for "this client exists but lacks the token-exchange grant" — a
  machine user is a valid `client_credentials` client yet is rejected for
  exchange.
- The **subject token must carry the app-project audience** (request scope
  `urn:zitadel:iam:org:project:id:{projectID}:aud`), or the exchange returns
  `subject_token invalid`. So `lib/oidcrp` requests that scope at login and
  retains the user's access token; `lib/svcauth` requests it on the
  exchanged token.
- **Delegation** (an explicit `actor_token`) additionally needs the instance
  **`enableImpersonation`** security setting; the control plane turns it on
  per tenant. The current flow uses the plain exchange (subject re-scoping):
  the exchanged token carries `sub` = the user and `azp` = the calling
  service's client — enough to represent "service S, for user U". The
  explicit `act` claim is available on top when the audit model wants it.

### Where each piece lives

- **`lib/svcauth`** (the convention): `Exchanger.OnBehalfOf(userToken)` signs
  a `private_key_jwt` assertion with the app key and performs the exchange,
  returning a token the caller presents to the callee. The callee-side
  validation middleware is added in s3.
- **`lib/oidcrp`**: retains the user's access token in the (in-memory,
  server-side) session and requests the project-audience scope, so a
  downstream service can exchange it.
- **Control plane**: provisions a per-app S2S client + JSON app key
  (`EnsureS2SClient` / `AddAppKey`) into a `<app>-s2s-key` Secret mounted for
  `lib/svcauth`, injects `OIDC_PROJECT_ID`, and enables impersonation per
  tenant. Provisioned only for apps that declare `Calls` (ADR-0016).
- **Audit** (ADR-0011, s3): the on-behalf-of service actor is recorded
  distinctly from the user subject.

### Service *authorization* stays out of the token layer

Which service may call which is enforced at the **network** layer
(ADR-0016's Cilium policy from `Calls`), not by per-service token scopes or
an OpenFGA service dimension. The token layer only proves identity + the
on-behalf-of user. OpenFGA still decides, on the callee, whether *the user*
may touch the resource.

## Consequences

- One app that calls another gains a second OIDC client (its S2S client)
  beyond its public login client. The user's login token must carry the
  project audience — a scope change in `lib/oidcrp`.
- The exchanged-token format follows the callee app's `accessTokenType`, so
  callees that want local JWT validation (s3) must issue JWT access tokens —
  tracked with the s3 validation work.
- App keys are create-only today (Zitadel returns the private key once);
  rotation is a later hardening, consistent with the DEK/DSN pattern.
- The acceptance is a real Ergonomos → Kamara on-behalf-of upload where the
  file lands **owned by the user** (s4), validating the model with its first
  consumer rather than on paper.

## Alternatives considered

- **Machine user + key (jwt-bearer / client_credentials)** — works for the
  service's *own* token but is rejected as the token-exchange client (see
  findings). Kept available (`AddMachineKey`) but not the S2S path.
- **BASIC / client_secret auth** — a working exchange client, but a shared
  secret at rest; `private_key_jwt` is strictly better for zero-trust and
  costs nothing extra (we already sign RS256). Rejected.
- **Forward the user token / impersonate-by-id** — rejected in R51 (above).
- **A service-caller dimension in OpenFGA / per-service token scopes** —
  rejected in ADR-0016 (R54): service topology is network-enforced.
