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
| 1 | ADRs first: URL/permalink + API-versioning conventions; control-plane architecture (CRD + controller, CR as source of truth). Go CI attaches |
| 2 | `Tenant` CRD + controller: namespace + CNPG cluster reconcile, delete/finalizers |
| 3 | Zitadel instance provisioning in the reconcile loop (System API from Go — M1 session-4 code moves here or into `lib/`) |
| 4 | App-pod deployment from the hardcoded catalog; tenant reachable end to end |
| 5 | HTMX UI: login, tenant list/create/delete; godog specs green |
| 6 | Buffer + writing: worklog, README updates, guidelines entry on controllers, demo recording |

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
