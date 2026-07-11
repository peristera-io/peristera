# ADR-0016: Cilium CNI + network-enforced service topology

- **Status:** accepted
- **Date:** 2026-07-06
- **Provenance:** Q&A Round 10 (R54, R55); `docs/m5-plan.md` (M5 session 1);
  folds issue #18. Part of the M5 service-to-service auth milestone.

## Context

M5 introduces **server-initiated app-to-app calls** (Ergonomos → Kamara,
and later OnlyOffice → Kamara). "Which service may call which" is a
platform-wide authorization question. R54 rejected two homes for it:

- **Per-service allowlists in app code** — sprawl: N services each with
  their own rules, no single place to read or revoke the graph.
- **OpenFGA** — service topology is **platform-uniform** (Ergonomos → Kamara
  holds in every tenant, identically), but OpenFGA stores are **per-tenant**
  and model **users → resources**. Putting the service graph there
  replicates identical tuples into every tenant store, sits on the hot path,
  and — because an OpenFGA `Check` is enforced *by the app* — gives weak
  containment when an app is compromised.

Meanwhile #18 recorded that cross-tenant isolation rested only on env
injection + DNS convention: with k3s's default flannel there is **no
network-level enforcement**, so a workload in tenant A can dial
`openfga.tenant-B.svc:8080` or another app directly.

## Decision

Service-topology authorization lives in **three orthogonal layers**; this
ADR owns the network layer.

1. **Network (this ADR) — "may A reach B at all?"** Enforced by the CNI,
   below the app, so a rogue/compromised service cannot open a socket to a
   peer it isn't cleared for. This is the topology allowlist and the
   containment boundary.
2. **Token (ADR for M5 s2/s3) — "is this really A, acting for user U?"**
   Authentication only; no per-service rules in app code.
3. **OpenFGA (unchanged) — "may user U touch this resource?"** Per-tenant,
   about people.

### Cilium is the CNI

The dev cluster (and the self-host / SaaS reference) runs **Cilium**, not
k3s's bundled flannel, because flannel does not enforce `NetworkPolicy`.
k3s is created with `--flannel-backend=none` and `--disable-network-policy`
so Cilium owns pod networking *and* policy. Cilium runs in **kube-proxy
coexistence** mode (`kubeProxyReplacement=false`): k3s ships an embedded
kube-proxy, and full replacement leaves the agent unable to reach the API
server. Installed via **helm**, not the `cilium` CLI, which on k3d/macOS
bakes the host-side kubeconfig API address into the pods. The exact,
reproducible steps live in `hack/dev-cluster.sh` (the self-hoster's recipe).

We use **plain Kubernetes `NetworkPolicy`**, not `CiliumNetworkPolicy` — the
topology we need (namespace isolation + per-app allow-lists) is expressible
in the portable API, so nothing ties us to Cilium at the manifest level;
Cilium is only the enforcement engine. Cilium L7 / identity policies remain
available if a future need (e.g. method-level scoping) justifies them.

### The graph is declared once, in the catalog contract

`CatalogApp` (ADR-0013) gains a **`Calls []string`** field naming the
sibling apps an app may invoke. It is the **single source of truth** for the
service graph. The control-plane reconciler generates the per-tenant
`NetworkPolicy` objects (and, in M5 s2, the Zitadel audience grants) from
it. `Calls` is platform-uniform, so it is expressed once in code and stamped
into each tenant namespace, never duplicated per tenant by hand.

**Create-only, today.** Provisioning writes these policies once at tenant
creation (`createIfAbsent`, ADR-0013 §3). Changing `Calls` in a later
release does **not** re-converge existing tenants — a new edge is denied and
a removed edge stays permitted until drift-correction (the 2027 control-plane
alpha) or a manual re-apply. This has a security consequence: the
"reconcile egress to deny-all" kill-switch below is **not automatic on
already-provisioned tenants** — it requires that same drift-correction (or a
manual policy edit) to take effect. Tracked with the netpol-hardening
follow-up.

### Policy shape (per tenant namespace)

- **Cross-tenant isolation:** pods accept traffic only from the same
  namespace or the ingress controller (`kube-system`). A workload in another
  tenant namespace cannot make a **direct** cross-tenant dial to *any*
  service — public or internal (verified: `neta -> netb` on both kamara and
  openfga is refused). (Addresses #18's network half.)
  **Known limit (not closed at L3/L4):** apps are allowed to egress to the
  shared ingress controller for their OIDC issuer path, and that controller
  routes by `Host` header to every tenant's *public* app Ingress. So a
  compromised app can reach another tenant's **publicly-exposed** app
  endpoints by bouncing an HTTP request off the shared ingress — the same
  reach any internet client has, and gated by those endpoints' own
  authentication, *not* by the network. Internal surfaces (OpenFGA,
  Postgres, any port without an Ingress) are **not** routable through the
  ingress and stay isolated. Closing the public-surface bounce needs L7/FQDN
  egress (`CiliumNetworkPolicy`) or routing the issuer path off the shared
  ingress — a deliberate follow-up, since until the token layer (s2/s3)
  ships, treating public endpoints as internet-reachable is the honest model.
- **Intra-namespace topology:** each app accepts app-to-app ingress only
  from the callers that declare it in `Calls` (plus the ingress controller
  for browser traffic). OpenFGA accepts traffic only from same-namespace
  apps.
- **Egress allow-list:** each app may egress only to its own namespace (its
  DB, OpenFGA, and declared `Calls` peers — the app-to-app leg is still gated
  on the callee's ingress), DNS, and the shared ingress controller (the OIDC
  issuer path). Cross-namespace and arbitrary-internet egress are denied, so
  a rogue app cannot reach another tenant or exfiltrate laterally — modulo
  the ingress-bounce limit above. The same-namespace and ingress-controller
  rules are pod-scoped but not port-scoped, so this is broader than a
  strict "issuer-only" list; tightening to L7 is the follow-up.
- **Identity is topology, not authentication:** these policies key on pod
  labels (`app.kubernetes.io/name`), which are self-asserted. They enforce
  *which labelled workload may reach which* — they do **not** authenticate
  that a pod is the real app. That guarantee rests on namespace
  pod-admission control (a tenant's default ServiceAccount cannot create
  pods) plus the token layer (s2/s3). Network labels answer "may A reach B",
  never "is this really A".

### Rogue-service kill-switch

Two central actions, the strong one kernel-enforced: disable the app's
Zitadel service account (M5 s2 — no more tokens), and/or reconcile its
egress `NetworkPolicy` to deny-all (enforced by Cilium regardless of what
the compromised app does). **Caveat (today):** because policy provisioning
is create-only (see above), the egress kill-switch is not automatic on an
already-running tenant — it needs drift-correction or a manual `kubectl`
edit of the policy. The kernel enforcement is real; the *automatic delivery*
of the changed policy is the gap, tracked with the netpol-hardening
follow-up.

## Consequences

- Cross-tenant network isolation (#18) is enforced, not assumed. **OpenFGA
  endpoint authentication** (the other half of #18) is handled alongside
  (preshared-key), so reaching an OpenFGA port no longer equals tuple
  read/write.
- The dev cluster now has a hard dependency on Cilium; **existing clusters
  must be recreated** (flannel can't be swapped in place). `hack/dev-cluster.sh`
  encodes the one-command path; CI needs the same CNI to exercise policy.
- k3d's default CNI *did* work with zero config; Cilium adds real setup cost
  (documented) — accepted because enforced isolation is a hard requirement
  before a real second tenant (MSP alpha) and OnlyOffice's S2S call.
- The service graph is auditable in one place (`Calls`) and reviewable in
  code review, not scattered across app configs.

## Alternatives considered

- **flannel + a separate policy engine (kube-router/Calico policy-only)** —
  more moving parts than adopting Cilium wholesale; Cilium also gives us the
  L7 headroom. Rejected.
- **OpenFGA service dimension / per-service token allowlists** — rejected in
  R54 (see Context).
- **Cilium kube-proxy replacement** — cleaner in theory, but fights k3s's
  embedded kube-proxy and broke API reachability on k3d. Deferred; not worth
  it for a dev cluster.
- **`CiliumNetworkPolicy` everywhere** — ties manifests to Cilium with no
  present benefit; use portable `NetworkPolicy` until L7 is actually needed.

## Amendment (2026-07-11, Q&A Round 14 R92, closes #43)

The **shared-ingress Host-header bounce** described under *Policy shape → Known
limit* is **accepted, not fixed** for now (#43 closed as such). The exposure is
"one tenant's compromised app can reach another tenant's *public* app endpoints
by bouncing an HTTP request off the shared ingress" — i.e. the same reach any
internet client already has, gated by each endpoint's own per-request
authentication. Internal surfaces (OpenFGA, Postgres, any port without an
Ingress) remain network-isolated and are unaffected.

The only real fix is L7/FQDN egress (`CiliumNetworkPolicy`) restricting each
app's issuer-path egress to its own issuer host, which would abandon this ADR's
deliberate "portable `NetworkPolicy` only" stance (§*Cilium is the CNI*) for
marginal gain over the authentication that already guards those endpoints.
**Decision:** keep the portable-policy stance; **revisit when the zero-trust /
token layer (ADR-0017 s2/s3) lands**, at which point per-service identity makes
an L7 egress rule cheap and principled rather than a Cilium-specific one-off.
Until then, treating public app endpoints as internet-reachable is the honest
threat model.
