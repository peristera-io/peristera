# Kubernetes controllers

How Peristera writes controller-runtime reconcilers. Controllers are a
named LLM thin spot (ADR-0002) — read this before touching one; every rule
below was earned in M2 (worklog 2026-07-03/04). Architecture rationale:
ADR-0008.

## Reconcile shape

1. **One durable step per pass.** Do a unit of work, persist its result in
   `status`, return a short `RequeueAfter`. Never block a worker waiting
   for an external system to converge.
2. **Status fields are the idempotency records** for non-idempotent
   external calls (`status.instanceId`, `status.clientId`): empty means
   "not done", set means "done — skip". Persist the record *before*
   dependent steps, and add an adoption path (search by natural key, e.g.
   custom domain) for when status is lost.
3. **Owner references + GC delete everything in-cluster**; a **finalizer
   exists only for what Kubernetes cannot collect** (a Zitadel instance,
   anything outside the cluster). In the finalizer, treat not-found as
   already-gone, requeue on transient errors.
4. **Expect projection/creation lag** in external systems: gate dependent
   steps on an explicit aliveness probe (e.g. OIDC discovery answering),
   not on the create call returning.
5. **Create-only vs. converge:** the skeleton creates resources it doesn't
   find; correcting drift and upgrades are product features with their own
   milestone — don't sneak them in.

## Wiring

- `For(primary)` + `Owns(secondary)` for everything you set an owner
  reference on — that's what re-triggers reconcile; unstructured types
  work (`SetGroupVersionKind` before `Owns`).
- **Leader election on** (`LeaderElectionID` per component) so a local
  dev run and the in-cluster deployment never double-reconcile. Any
  Runnable that must serve on every replica (HTTP servers!) implements
  `NeedLeaderElection() bool { return false }` — otherwise rolling
  updates deadlock on the readiness probe.
- Validation belongs in the CRD, enforced by the API server: CEL for
  immutability (`self == oldSelf`) and reserved values (`!(self in
  [...])`), patterns for shape. `ValidSlug`-style Go helpers are for
  friendly errors, never the authority.

## Dev loop

- Spec first: godog features drive the loop (`PERISTERA_E2E=1` against
  the dev cluster — `hack/dev-cluster.sh` brings it up).
- Regenerate after changing `apis/`: controller-gen `object` + `crd`
  (see control-plane README).
- `go run` children survive `pkill -f cmd/...` — kill by port
  (`lsof -ti:8080`) or a zombie controller will keep reconciling under
  your feet.
