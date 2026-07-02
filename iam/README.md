# Peristera IAM

Login, users, and OIDC for every Peristera app — a **branded layer over
Zitadel** (decided, all-in: ADR-0004). One shared Zitadel deployment serves
one **virtual instance per tenant**; a tenant can be broken out to a
dedicated deployment when resources or legal requirements demand it
(topology and break-out seam: `docs/m1-plan.md`, confirmed by ADR-0006).

**Status: M1 spike in progress.** This folder currently holds the dev
deployment and (next) the OIDC stub relying party. It becomes the real
Peristera IAM service from M2 onward.

License: AGPL-3.0-or-later with the Peristera App Store distribution
exception (`LICENSE-EXCEPTION.md`). Read the monorepo `README.md` first —
it is the operating manual.

## Dev environment (M1 spike, session 1)

Everything runs on a local k3d cluster; k3s is the deployment contract
(ADR-0003). Prerequisites: Docker, `k3d`, `kubectl`, `helm`.

```sh
# 1. Cluster. Host ports 9080/9443 → Traefik ingress (80/443 need root).
k3d cluster create peristera-dev \
  --port "9080:80@loadbalancer" --port "9443:443@loadbalancer" --wait
# macOS: k3d writes the API server as 0.0.0.0, which macOS won't dial:
kubectl config set clusters.k3d-peristera-dev.server \
  "https://127.0.0.1:$(kubectl config view \
    -o jsonpath='{.clusters[?(@.name=="k3d-peristera-dev")].cluster.server}' \
    | sed 's/.*://')"

# 2. Platform namespace + CloudNativePG operator.
kubectl create namespace peristera-system
helm repo add cnpg https://cloudnative-pg.github.io/charts
helm repo add zitadel https://charts.zitadel.com
helm install cnpg cnpg/cloudnative-pg -n cnpg-system --create-namespace --wait

# 3. Zitadel's Postgres.
kubectl apply -f deploy/dev/cnpg-zitadel.yaml
kubectl wait --for=condition=Ready cluster/zitadel-db \
  -n peristera-system --timeout=240s

# 4. DSN secret from the CNPG-generated credentials (DSN mode).
PW=$(kubectl get secret zitadel-db-app -n peristera-system \
  -o jsonpath='{.data.password}' | base64 -d)
kubectl create secret generic zitadel-db-dsn -n peristera-system \
  --from-literal=dsn="postgresql://zitadel:${PW}@zitadel-db-rw.peristera-system.svc.cluster.local:5432/zitadel?sslmode=require"

# 5. Zitadel + Login v2.
helm install zitadel zitadel/zitadel \
  -n peristera-system -f deploy/dev/zitadel-values.yaml

# 6. Verify.
curl http://iam.127.0.0.1.sslip.io:9080/debug/healthz          # 200 ok
curl http://iam.127.0.0.1.sslip.io:9080/.well-known/openid-configuration
open http://iam.127.0.0.1.sslip.io:9080/ui/v2/login/loginname  # Login v2
```

`*.127.0.0.1.sslip.io` is public wildcard DNS resolving to `127.0.0.1` —
each tenant's virtual instance gets its own such domain (domain-per-tenant
is a day-one rule; the issuer URL must never change).

The Helm chart creates an `iam-admin` machine user (IAM_OWNER) with its key
in the `iam-admin` Kubernetes secret — that is the Management/Admin API
seam. Creating *virtual instances* needs the System API (session 2).

## Layout

```text
iam/
├── README.md            ← this file
└── deploy/dev/          ← local k3d manifests + Helm values (M1 spike)
```
