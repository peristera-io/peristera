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

**In-cluster DNS (required once tenant app pods exist):** inside a pod,
`127.0.0.1` is the pod itself, so those domains must resolve to Traefik
instead — apply `deploy/dev/coredns-sslip.yaml` (instructions in the
file, including the Traefik `:9080` service port the issuer URLs need).

The Helm chart creates an `iam-admin` machine user (IAM_OWNER) with its key
in the `iam-admin` Kubernetes secret — that is the Management/Admin API
seam. Creating *virtual instances* needs the System API, below.

## Virtual instances via the System API (session 2)

One shared deployment, one virtual instance per tenant, selected by host
header. The System API authenticates with a self-signed RS256 JWT; the
`admin-client` system user (role `SYSTEM_OWNER` — `IAM_OWNER` is *not*
enough for instance operations) is declared in `zitadel-values.yaml`, its
certificate mounted from a secret:

```sh
# One-time: keypair + cert secret, then helm upgrade with the values file.
openssl req -x509 -newkey rsa:2048 -keyout admin-client.key \
  -out admin-client.crt -nodes -days 365 -subj "/CN=admin-client"
kubectl create secret generic admin-client-tls -n peristera-system \
  --from-file=tls.crt=admin-client.crt
helm upgrade zitadel zitadel/zitadel \
  -n peristera-system -f deploy/dev/zitadel-values.yaml

# Self-signed system JWT (aud = the default instance's issuer).
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
NOW=$(date +%s)
H=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
P=$(printf '{"iss":"admin-client","sub":"admin-client","aud":"http://iam.127.0.0.1.sslip.io:9080","iat":%s,"exp":%s}' "$NOW" $((NOW+3600)) | b64url)
S=$(printf '%s.%s' "$H" "$P" | openssl dgst -sha256 -sign admin-client.key -binary | b64url)
TOKEN="$H.$P.$S"

# Tenant instance: own domain (day-one rule), own first org + owner user.
curl -X POST "http://iam.127.0.0.1.sslip.io:9080/system/v1/instances/_create" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{
  "instanceName": "tenant-demo",
  "firstOrgName": "Demo GmbH",
  "customDomain": "demo.127.0.0.1.sslip.io",
  "human": {
    "userName": "demo-admin",
    "email": {"email": "admin@demo.example", "isEmailVerified": true},
    "profile": {"firstName": "Demo", "lastName": "Admin"},
    "password": {"password": "…", "passwordChangeRequired": false}
  }}'

curl http://demo.127.0.0.1.sslip.io:9080/.well-known/openid-configuration
# → issuer http://demo.127.0.0.1.sslip.io:9080 — per-tenant issuer, shared pods
# Delete: DELETE /system/v1/instances/{id}. Note: a just-created instance
# 404s for a few seconds (projection lag) — retry briefly.
```

Verified findings (2026-07-02): tenant instance serves its own issuer on
its own domain; the *shared* Login v2 deployment serves it by host; idle
footprint is flat when instances are added (~240–260 Mi for the whole
Zitadel + login + Postgres set — the 512 MB floor is per deployment, not
per virtual instance). Full evidence goes into ADR-0006.

## The stub relying party (session 3)

`cmd/stub` is the OIDC login shape every Peristera app copies: auth-code +
PKCE against a tenant instance's issuer, session cookie, logout round trip.

```sh
# Register the client on the tenant instance (system JWT as above, but
# note the audience gotcha below; ORG = the tenant's first org id from
# POST /admin/v1/orgs/_search):
#   POST /management/v1/projects                     {"name":"peristera"}
#   POST /management/v1/projects/{id}/apps/oidc      → clientId
#     (public client: authMethodType NONE, PKCE; devMode=true for http;
#      idTokenUserinfoAssertion=true, else name/email claims are empty)
STUB_CLIENT_ID=<clientId> go run ./cmd/stub
open http://localhost:5556/            # log in as demo-admin
node e2e/login-e2e.mjs                 # headless E2E (needs playwright)
```

Gotchas that cost real time — all destined for ADR-0006 and the M2
provisioning sequence:

1. **System-JWT audience is *always* the deployment's ExternalDomain
   issuer** (`http://iam.…:9080`), even when calling a *tenant* instance's
   management/admin APIs (the tenant issuer as audience → 401).
2. **Roles ride on the `System` membership.** `SYSTEM_OWNER` alone covers
   only the System API; add `IAM_OWNER` to the *same* `MemberType: System`
   entry for admin/management APIs inside instances. A separate
   `MemberType: IAM` entry does **not** work — it wants a per-instance
   AggregateID.
3. **New instances must trust the login's domain**: Login v2 calls the API
   under `ZITADEL_EXTERNALDOMAIN`, and a fresh virtual instance doesn't
   trust it (login page → 500). Fix, per tenant, at provisioning time:
   `POST /v2beta/instances/{id}/trusted-domains {"domain":"iam.…"}`.

So the control plane's tenant-IAM sequence is: create instance → add
trusted domain → create project + PKCE app → hand the clientId to the
tenant's app pods.

## Layout

```text
iam/
├── README.md            ← this file
├── cmd/stub/            ← OIDC relying-party stub (the login pattern)
├── e2e/                 ← headless login E2E (playwright)
└── deploy/dev/          ← local k3d manifests + Helm values (M1 spike)
```
