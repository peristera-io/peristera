# ADR-0020: Public deployment — Scaleway single-node k3s, OpenTofu, per-host TLS

- **Status:** accepted
- **Date:** 2026-07-07
- **Provenance:** M7 plan (`docs/m7-plan.md`); Q&A Round 13 (R76–R89); the M7
  de-risk spike (real Let's Encrypt cert on `hello.peristera.app`, then torn
  down). First deployment ADR; the http→https half amends ADR-0006/0007 (issuer
  scheme). Sits above ADR-0016 (Cilium), ADR-0019 (operator authZ).

## Context

M6 shipped the platform on a local k3d cluster. M7 puts it on the public
internet as a demo/SaaS: an operator provisions tenants that serve Kamara,
Ergonomos, and the opt-in office engine over real TLS at
`<app>.<tenant>.peristera.app` (model A). This ADR records the deployment
architecture — the pieces below were settled in Q&A Round 13 and proven by the
spike, and this is where they live so the plan doc can die when M7 ships.

## Decision

### Topology — one Scaleway instance running k3s, a Flexible IP, no LB

A **single Scaleway Instance** (`PLAY2-MICRO`, 8 GB — the full stack plus
Collabora needs it) runs **k3s**, fronted by a **reserved Flexible IP**. No
managed Load Balancer: k3s's bundled Traefik + servicelb bind the node's
ports 80/443, and the Flexible IP gives a **stable ingress address** that DNS
points at once, permanently. This is the cheapest shape that clears the budget
target and it **mirrors dev** (k3d→k3s), so the same manifests and the same
CNI (Cilium) carry over. Kapsule + a managed LB (a ~€50/mo floor for hands-off
multi-node autoscaling) is deferred to when one node is genuinely not enough.

Cost is **on-demand**: ~€0.055/hr running; **stop** the instance to pause at
~€2/mo with state intact; `tofu destroy` to zero. Budget target < €50, ideally
< €20 — met by stop/start, not by a smaller node.

### Provisioning — OpenTofu for infra, a bootstrap script for the platform

**OpenTofu** (`deploy/scaleway/`) provisions only cloud primitives: the
instance + Flexible IP, a **firewall** (80/443 to the world; SSH + the k3s API
6443 to `admin_cidr` only — Scaleway leaves 6443 internet-open by default, so
this is load-bearing), **Object Storage** buckets (blobs, backups), the Tofu
**state backend** (Object Storage, two-phase — the bucket is created out of
band so `destroy` never eats its own state), and **Secret Manager** entries for
the platform secrets it generates (Zitadel masterkey, cp-openfga token,
admin-client keypair — born in Tofu, never on disk or in git).

The **platform** is brought up by `deploy/scaleway/bootstrap.sh`, the cloud
twin of `hack/dev-cluster.sh`, run against the node's kubeconfig: Cilium →
cert-manager (+ LE issuer) → external-dns → ESO → CNPG → Zitadel → cp-openfga →
control-plane → operator seed. Helm/manifests, not Tofu-driven Helm — the same
ordering and components as dev, so the two environments do not drift.

### Secrets — Scaleway Secret Manager, synced by ESO

Platform secrets live in **Scaleway Secret Manager** and are pulled into the
cluster by the **External Secrets Operator** as native Secrets. Nothing
sensitive is templated into a manifest or committed. The **one** directly
injected secret is the Scaleway API credential ESO itself needs to reach Secret
Manager (and external-dns needs to write DNS) — the unavoidable bootstrap root.

### TLS & DNS — per-host Let's Encrypt (HTTP-01), external-dns

**cert-manager + Let's Encrypt HTTP-01** via Traefik issues a **per-host** cert
(the spike proved ~40s end-to-end). **No wildcard cert, no DNS-01 webhook** —
model A only ever needs per-host certs. **external-dns** (Scaleway provider)
writes the A records (`cp`, `iam`, and each tenant host) into the delegated
`peristera.app` zone, so records track ingresses automatically. Because
HTTP-01 cannot issue a wildcard, the shared Zitadel ingress carries only its
own host (`iam.<domain>`); each **tenant virtual-instance** issuer host gets its
**own** ingress + cert, created by the control plane at provision time (s2).

### http → https — "scheme is config"

The control plane and apps previously hardcoded `http://` (the tenant issuer
must equal the token `iss`). M7 makes the scheme configurable (`TENANT_SCHEME`,
`TENANT_EXTERNAL_PORT`) so tenants serve `https://` on the cloud while dev stays
`http://`. Amends the issuer-URL assumptions in ADR-0006/0007.

### Images — ghcr, pulled

Our images are built amd64 and pushed to `ghcr.io/peristera-io/<app>` by CI
(public — the repo is AGPL, no pull secret). Dev keeps `k3d image import`; the
cloud control plane resolves app images from `IMAGE_PREFIX`/`IMAGE_TAG`
(`ghcr.io/peristera-io/<app>:<tag>`).

## Consequences

- **Single node = single point of failure**, no HA: one instance, one CNPG
  replica. Accepted for a demo/early-SaaS; HA rides in with Kapsule + LB later.
  Backups (CNPG dumps + blob lifecycle to Object Storage) are the durability
  floor until then.
- **Hairpin DNS avoided:** in-cluster clients (control plane → Zitadel) would
  otherwise resolve the public host to the node's own Flexible IP and rely on
  NAT loopback, which Scaleway may not support. `bootstrap.sh` installs a
  CoreDNS override (the `coredns-custom` ConfigMap, twin of dev's
  `coredns-sslip`) so `iam`/`cp.<domain>` resolve to the cluster-internal
  Traefik; Traefik still serves the real cert by SNI. Public DNS (external-dns)
  and Let's Encrypt validation are unaffected. s2 extends the rewrite to tenant
  issuer hosts.
- **Firewall is the exposure boundary** for the k3s API; a wrong/blank
  `admin_cidr` either locks out admin or (if widened) exposes 6443. The default
  is a deliberately unusable placeholder that fails closed.
- **Stop/start, not always-on** is the cost model; a stopped instance keeps
  its volume + Flexible IP but serves nothing. Documented in the runbook.
- Two environments, one shape: dev (k3d, http, `:dev` images, literal secrets)
  and cloud (k3s, https, ghcr images, Secret-Manager secrets) share components,
  ordering, and CNI — divergence is confined to config.

## Alternatives rejected

- **Kapsule + managed LB:** hands-off and multi-node, but a ~€50/mo floor and a
  LoadBalancer-shaped ingress that fights the stable-IP/no-LB/budget targets.
  Deferred, not dropped — it is the scale story.
- **Wildcard cert (DNS-01 + Scaleway webhook):** one cert for `*.<domain>`, but
  needs a DNS-01 solver and a wildcard the spike showed is unnecessary; per-host
  HTTP-01 is simpler and sufficient for model A.
- **Secrets in Tofu state / sealed-secrets in git:** rejected — the repo is
  public and state can hold plaintext; Secret Manager + ESO keeps secrets out of
  both.
- **All-Tofu (Kubernetes/Helm providers):** one `apply` for infra + platform,
  but it couples cluster bring-up to provider quirks and diverges from the dev
  script; a thin infra-only Tofu + a shared-shape bootstrap script keeps dev and
  cloud honest.
