# M7 plan — public demo / SaaS on Scaleway (OpenTofu)

- **Status:** planning (2026-07-07). Parameters settled in `Q&A.md` Round 13
  (R76–R89) + the de-risk spike (below). Runs after M6 (done) + the pre-M7
  hardening batch (done, merged). Design homes: a new ADR for the deployment
  architecture (Scaleway/k3s/TLS topology), amendments where the http→https
  scheme migration touches ADR-0006/0007. This plan dies when M7 ships.

## Goal

Peristera on the public internet. **OpenTofu** provisions a Scaleway host + the
platform; a **single operator**, created at deploy, provisions tenants with an
**optional domain** + an admin account. A tenant's users log in and use Kamara,
Ergonomos, and (opt-in) the office engine over **real Let's Encrypt TLS** at
`<app>.<tenant>.peristera.app`. A "what/why" landing page lives at
`peristera.io`. The last step is **custom-domain tenants** (`peristera.lu`),
which are load-bearing for proper deployments.

## Settled decisions (Q&A Round 13 + spike, 2026-07-07)

- **Domains:** `.io` = marketing + landing (later sign-up); `.app` = where
  tenants run (`<app>.<tenant>.peristera.app`, **model A**, R77); `.dev` =
  open-source/developer hub; `.lu` = flagship **custom-domain** dogfood tenant +
  EU-sovereignty showcase. `peristera.app` is **delegated to Scaleway DNS**.
- **Topology:** a **single Scaleway instance running k3s + a reserved Flexible
  IP, no managed Load Balancer** — stable ingress IP, cheapest, and it mirrors
  the dev k3s environment. Kapsule + LB (a €50/mo floor, hands-off multi-node
  autoscaling) is deferred to when one node isn't enough. Cost model is
  **on-demand**: ~€0.055/hr (8 GB) running; **stop the instance** to pause at
  ~€2/mo with state intact; `tofu destroy` to zero.
- **TLS (spike-proven):** **cert-manager + Let's Encrypt HTTP-01** via k3s
  Traefik issues a real per-host cert in ~40s. **No wildcard cert / no Scaleway
  DNS-01 webhook needed** — per-host HTTP-01 covers model A. **external-dns**
  (Scaleway provider) creates the per-tenant A records.
- **Registry:** images on **`ghcr.io/peristera-io/<app>`** (public — the repo is
  AGPL; no pull secret) built/pushed by **GitHub Actions**. CI builds **amd64**
  (the Scaleway node is x64; dev builds arm64 locally).
- **http→https:** a **"scheme is config" pass** (R78) — the control plane + apps
  hardcode `http://` (the tenant issuer must equal the token `iss`); M7 makes
  the scheme configurable so tenants serve `https://`.
- **Secrets & state:** Tofu **state** in a Scaleway **Object Storage** backend;
  platform **secrets** in **Scaleway Secret Manager**, synced into the cluster
  by the **External Secrets Operator (ESO)** — nothing sensitive in the (public)
  repo.
- **Storage:** Kamara blobs → **Scaleway Object Storage (S3)** (R83, deferred
  #21; blobs are already XChaCha20-encrypted before storage). Postgres stays
  **CNPG** (R84). **Minimal backups** — CNPG scheduled dumps + blob lifecycle to
  Object Storage (R85).
- **Provisioning:** **operator-provisioned tenants only**, no self-serve sign-up
  (R80). Tenant creation takes an **optional** domain — none → `<slug>.peristera.app`; a custom apex → that domain (R81). The **first operator is
  Tofu-seeded** (R81, building on the merged #1 operator-authz).
- **Landing:** one `peristera.io` page — what Peristera is and why (R87).
- **Budget:** < €50, ideally < €20 — achieved via on-demand/stop-start, not a
  smaller node (the full stack + Collabora needs ~8 GB) (R88).

## Sessions

| Session | Work |
|---|---|
| **0** | **Images on ghcr.io.** A GitHub Actions workflow builds + pushes `control-plane`, `kamara`, `ergonomos`, `stub` to `ghcr.io/peristera-io/<app>` (amd64), tagged. Manifests/catalog reference the registry (a `IMAGE_REGISTRY`/tag knob) instead of `:dev`. Dev keeps `k3d image import`; cloud pulls from ghcr. |
| **1** | **Tofu full infra + platform bootstrap + https.** Tofu: instance + Flexible IP + Object Storage (blobs/backups) + **state backend** + **Secret Manager** entries + **firewall 6443** to a trusted CIDR. Bootstrap (Helm/manifests, Tofu- or script-driven): k3s + **Cilium** (cross-tenant NetworkPolicy, as in dev) + Traefik + **cert-manager** (LE issuer) + **external-dns** + **ESO** + CNPG operator + Zitadel + control-plane + cp-openfga. Land the **http→https "scheme is config"** migration and verify the platform serves `https://cp.peristera.app` with a real cert. |
| **2** | **First operator → one tenant on real TLS.** Tofu seeds the operator (Secret Manager → Zitadel user + `OPERATOR_SUBJECTS`). The operator provisions **one tenant** (optional-domain flow, default `<slug>.peristera.app`); external-dns creates records, cert-manager issues per-host certs; Kamara/Ergonomos (+ opt-in office) work over **https** end to end. This is the M7 acceptance. |
| **3** | **Landing page.** A single static `peristera.io` "what/why" page (served simply — Traefik static, or a tiny container). Wire DNS. First build-in-public post optional. |
| **4** | **Custom domains (final).** BYO-domain tenants (`peristera.lu`): domain verification + external-dns/cert per custom apex, the optional-domain path from s2 extended. The load-bearing step for real deployments. |
| — | **Threaded:** backups (CNPG + blob lifecycle) live by s1/s2; the deployment ADR written alongside s1; teardown/stop-start runbook documented. |

## Definition of done

- [ ] Images build + push to ghcr.io via CI; the cloud pulls them.
- [ ] `tofu apply` from zero yields a running platform on Scaleway with real
      TLS, state in Object Storage, secrets in Secret Manager; `tofu destroy`
      leaves nothing billing.
- [ ] The Tofu-seeded operator logs into `https://cp.peristera.app`, creates a
      tenant (optional domain), and that tenant's users log in + use Kamara +
      Ergonomos (+ office) over `https://<app>.<tenant>.peristera.app`.
- [ ] A `peristera.io` landing page is live.
- [ ] A custom-domain tenant (`peristera.lu`) works end to end.
- [ ] Backups run; the k3s API is firewalled; Cilium enforces cross-tenant
      isolation (as verified in dev).
- [ ] Deployment ADR + stop/start + teardown runbook written.

## Out of scope (deferred, not dropped)

Public self-serve sign-up; multi-node / autoscaling / HA (Kapsule + LB — the
scale story); billing/metering/usage pricing (the SaaS-revenue era); the `.dev`
docs site polish; federation (2027). Each is additive on top of a working
single-node public deploy.
