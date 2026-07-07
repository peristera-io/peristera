# Scaleway deploy (M7)

OpenTofu for the Peristera public deployment. **Spike scope** (R89): a single
Scaleway instance running k3s + a stable Flexible IP + Object Storage, to
de-risk the domain / cert / https trio before the full plan.

## Topology (why k3s, not Kapsule)

A single instance + k3s + a reserved Flexible IP gives a **stable ingress IP
with no managed Load Balancer** (~€10/mo saved) and mirrors the dev
environment (k3d→k3s). Kapsule's managed control plane is free but expects a
LoadBalancer service for ingress, which fights the "no LB / stable IP / <€50"
targets. Node `PLAY2-MICRO` (8 GB) ≈ €0.055/hr (~€40/mo if left running); the
spike creates → verifies → **destroys**, so it costs cents.

## Guardrails

- Secrets/state never touch git: creds come from `../../.env.scaleway`
  (gitignored); state is local for the spike, Object Storage backend for real
  deploys; platform secrets go to Scaleway Secret Manager.
- Always `plan` before `apply`; `destroy` after the spike. A budget alert is
  set on the account as the backstop.

## Use

```sh
set -a && . ../../.env.scaleway && set +a   # load SCW_* creds
tofu init
tofu plan                                    # review resources + cost
tofu apply                                   # provision (spends money)
# ... fetch kubeconfig (see the kubeconfig_hint output), bootstrap platform ...
tofu destroy                                 # tear it all down
```
