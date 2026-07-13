#!/usr/bin/env bash
# Bring up the full Peristera platform on the Scaleway k3s node — the cloud
# twin of hack/dev-cluster.sh. Run it AFTER `tofu apply` has created the node,
# the Flexible IP, and the Secret Manager entries, against the node's
# kubeconfig. Idempotent: safe to re-run.
#
#   set -a && . ../../.env.scaleway && set +a          # SCW_* creds
#   export KUBECONFIG=/tmp/peristera.kubeconfig        # from `tofu output kubeconfig_hint`
#   export DOMAIN=$(tofu output -raw domain)
#   export LE_EMAIL=you@example.org                    # ACME contact
#   export IMAGE_TAG=0.0.1-alpha                        # ghcr image tag to deploy
#   ./bootstrap.sh
#
# The stack mirrors dev component-for-component, on real https:
#   Cilium → cert-manager(+LE issuer) → external-dns → ESO → CNPG → Zitadel →
#   cp-openfga → control-plane → operator seed.
set -euo pipefail
cd "$(dirname "$0")"

NS=peristera-system
: "${DOMAIN:?set DOMAIN (e.g. peristera.app)}"
: "${LE_EMAIL:?set LE_EMAIL (ACME account contact)}"
: "${IMAGE_TAG:?set IMAGE_TAG (ghcr image tag, e.g. 0.0.1-alpha)}"
: "${SCW_ACCESS_KEY:?source ../../.env.scaleway first}"
: "${SCW_SECRET_KEY:?source ../../.env.scaleway first}"
: "${SCW_DEFAULT_PROJECT_ID:?source ../../.env.scaleway first}"
: "${SCW_DEFAULT_ORGANIZATION_ID:?source ../../.env.scaleway first}"
SCW_REGION="${SCW_REGION:-fr-par}"
# The public marketing/landing apex (s3). Must also be delegated to Scaleway
# DNS so external-dns can publish it and cert-manager can issue its cert.
LANDING_DOMAIN="${LANDING_DOMAIN:-peristera.io}"
# Object Storage bucket CNPG streams Postgres backups to (R85). Defaults to the
# Tofu output if not set explicitly.
BACKUPS_BUCKET="${BACKUPS_BUCKET:-$(tofu output -raw backups_bucket 2>/dev/null || true)}"
: "${BACKUPS_BUCKET:?set BACKUPS_BUCKET (e.g. tofu output -raw backups_bucket)}"
# Blob backup + secret escrow (#59/#77): the age PUBLIC key backup jobs encrypt
# escrowed Secrets to. Generate once with `age-keygen`; keep the private key
# OUT of the cluster and the repo (password manager) — it is the only way to
# decrypt a restored DEK/masterkey. Optional BACKUP_HEARTBEAT_URL is pinged
# after each successful backup job (e.g. healthchecks.io).
: "${BACKUP_AGE_RECIPIENT:?set BACKUP_AGE_RECIPIENT (age public key, from age-keygen — private key stays out of the cluster)}"
BACKUP_HEARTBEAT_URL="${BACKUP_HEARTBEAT_URL:-}"
export DOMAIN LE_EMAIL IMAGE_TAG SCW_REGION SCW_DEFAULT_PROJECT_ID LANDING_DOMAIN BACKUPS_BUCKET BACKUP_AGE_RECIPIENT BACKUP_HEARTBEAT_URL

CILIUM_VERSION=${CILIUM_VERSION:-1.19.4}
CERT_MANAGER_VERSION=${CERT_MANAGER_VERSION:-v1.16.2}
ESO_VERSION=${ESO_VERSION:-0.10.5}
EXTERNAL_DNS_VERSION=${EXTERNAL_DNS_VERSION:-1.15.0}
# Pin CNPG to a 1.30 chart: in-tree barman-cloud backups (what we use) are
# removed in operator 1.31 (migrate to the barman-cloud plugin — follow-up).
CNPG_CHART_VERSION=${CNPG_CHART_VERSION:-0.29.0}

# envsubst limited to our named placeholders, so nothing else in the manifests
# is accidentally expanded.
SUBST='${DOMAIN} ${LE_EMAIL} ${IMAGE_TAG} ${SCW_REGION} ${SCW_DEFAULT_PROJECT_ID} ${LANDING_DOMAIN} ${BACKUPS_BUCKET} ${BACKUP_AGE_RECIPIENT} ${BACKUP_HEARTBEAT_URL}'
apply_tmpl() { envsubst "$SUBST" < "$1" | kubectl apply -f - ; }

echo "==> helm repos"
helm repo add cilium https://helm.cilium.io/ >/dev/null 2>&1 || true
helm repo add cnpg https://cloudnative-pg.github.io/charts >/dev/null 2>&1 || true
helm repo add zitadel https://charts.zitadel.com >/dev/null 2>&1 || true
helm repo add jetstack https://charts.jetstack.io >/dev/null 2>&1 || true
helm repo add external-secrets https://charts.external-secrets.io >/dev/null 2>&1 || true
helm repo add external-dns https://kubernetes-sigs.github.io/external-dns/ >/dev/null 2>&1 || true
helm repo add scaleway https://helm.scw.cloud/ >/dev/null 2>&1 || true
helm repo update >/dev/null

echo "==> cilium CNI"
# k3s ships an embedded kube-proxy, so Cilium must not replace it (as in dev).
# The node is NotReady until Cilium lands (flannel disabled in cloud-init).
helm upgrade --install cilium cilium/cilium --version "$CILIUM_VERSION" -n kube-system \
  --set kubeProxyReplacement=false --set operator.replicas=1 --wait --timeout 5m
kubectl wait --for=condition=Ready node --all --timeout=300s >/dev/null

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "==> scaleway root credential (for ESO + external-dns)"
# The one secret we inject directly — ESO can't fetch the credential it needs
# to reach Secret Manager. Everything else comes FROM Secret Manager via ESO.
# It lives in BOTH consumer namespaces: ESO's ClusterSecretStore reads it from
# peristera-system (pinned), and external-dns reads it namespace-locally from
# its own namespace (secretKeyRef is not cross-namespace).
scaleway_secret_in() {
  kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl create secret generic scaleway-secret -n "$1" \
    --from-literal=SCW_ACCESS_KEY="$SCW_ACCESS_KEY" \
    --from-literal=SCW_SECRET_KEY="$SCW_SECRET_KEY" \
    --from-literal=SCW_DEFAULT_PROJECT_ID="$SCW_DEFAULT_PROJECT_ID" \
    --from-literal=SCW_DEFAULT_ORGANIZATION_ID="$SCW_DEFAULT_ORGANIZATION_ID" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
}
scaleway_secret_in "$NS"
scaleway_secret_in external-dns

echo "==> external secrets operator"
helm upgrade --install external-secrets external-secrets/external-secrets \
  -n external-secrets --create-namespace --version "$ESO_VERSION" \
  --set installCRDs=true --wait --timeout 5m
apply_tmpl manifests/eso.yaml
# Block until the platform secrets have actually materialised — Zitadel's chart
# mounts the masterkey + admin-client cert, so they must exist first.
for es in zitadel-masterkey admin-client-tls cp-openfga-authn-key; do
  kubectl wait --for=condition=Ready "externalsecret/$es" -n "$NS" --timeout=180s
done

echo "==> cert-manager + Scaleway DNS-01 webhook + Let's Encrypt issuer"
helm upgrade --install cert-manager jetstack/cert-manager -n cert-manager \
  --create-namespace --version "$CERT_MANAGER_VERSION" \
  --set crds.enabled=true --wait --timeout 5m
# DNS-01 solver (ADR-0021, #52): the Scaleway webhook + the API creds in the
# cert-manager namespace, where cert-manager resolves ClusterIssuer DNS-01
# secret refs. DNS-01 needs no HTTP reachability, so there is no external-dns
# first-issue race and no healTenantCerts self-heal.
helm upgrade --install scaleway-certmanager-webhook scaleway/scaleway-certmanager-webhook \
  -n cert-manager --wait --timeout 5m
scaleway_secret_in cert-manager
apply_tmpl manifests/cert-manager-issuer.yaml

echo "==> external-dns (Scaleway zone for $DOMAIN)"
envsubst "$SUBST" < manifests/external-dns-values.yaml > /tmp/external-dns-values.yaml
helm upgrade --install external-dns external-dns/external-dns \
  -n external-dns --create-namespace --version "$EXTERNAL_DNS_VERSION" \
  -f /tmp/external-dns-values.yaml --wait --timeout 5m

echo "==> in-cluster DNS override (avoid hairpin to the public IP)"
# So the control plane reaches https://iam.$DOMAIN via cluster-internal Traefik
# instead of NAT-looping through the node's own Flexible IP. Twin of dev's
# coredns-sslip. Public DNS (external-dns) is unaffected — browsers and Let's
# Encrypt still hit the real IP.
apply_tmpl manifests/coredns-custom.yaml
kubectl rollout restart deploy/coredns -n kube-system >/dev/null
kubectl rollout status deploy/coredns -n kube-system --timeout=120s

echo "==> cloudnative-pg + zitadel database"
helm upgrade --install cnpg cnpg/cloudnative-pg -n cnpg-system --create-namespace \
  --version "$CNPG_CHART_VERSION" --wait
apply_tmpl manifests/cnpg-zitadel.yaml
kubectl wait --for=condition=Ready cluster/zitadel-db -n "$NS" --timeout=300s
if ! kubectl get secret zitadel-db-dsn -n "$NS" >/dev/null 2>&1; then
  PW=$(kubectl get secret zitadel-db-app -n "$NS" -o jsonpath='{.data.password}' | base64 -d)
  kubectl create secret generic zitadel-db-dsn -n "$NS" \
    --from-literal=dsn="postgresql://zitadel:${PW}@zitadel-db-rw.${NS}.svc.cluster.local:5432/zitadel?sslmode=require"
fi

echo "==> zitadel + login v2 (https at iam.$DOMAIN)"
envsubst "$SUBST" < manifests/zitadel-values.yaml > /tmp/zitadel-values.yaml
helm upgrade --install zitadel zitadel/zitadel -n "$NS" -f /tmp/zitadel-values.yaml --timeout 10m
kubectl rollout status deploy/zitadel -n "$NS" --timeout=600s
kubectl rollout status deploy/zitadel-login -n "$NS" --timeout=300s

echo "==> control plane + cp-openfga"
kubectl apply -f ../../control-plane/deploy/crd/peristera.io_tenants.yaml
kubectl apply -f manifests/cp-openfga.yaml
apply_tmpl manifests/control-plane.yaml
# Nightly age-encrypted escrow of the bootstrap Secrets (zitadel-masterkey,
# admin-client-tls, cp-openfga-authn-key) a restore cannot regenerate (#77).
apply_tmpl manifests/secret-escrow.yaml
kubectl rollout status deploy/cp-openfga -n "$NS" --timeout=180s

echo "==> waiting for the platform certificate (cp.$DOMAIN)"
# HTTP-01 needs external-dns to have published cp.$DOMAIN → node IP first;
# cert-manager retries until DNS propagates, so give it room.
kubectl wait --for=condition=Ready certificate/control-plane-tls -n "$NS" --timeout=600s || \
  echo "    (cert not Ready yet — check DNS delegation for $DOMAIN and \`kubectl describe certificate\`)"

echo "==> seed control-plane operator (ADR-0019)"
# The operator is Zitadel's iam-admin machine user; resolve its sub from the
# chart-issued PAT via userinfo (over real https now) and inject it. The
# iam-admin-pat secret is emitted by the Zitadel chart's default FirstInstance,
# exactly as in dev (hack/dev-cluster.sh reads the same secret) — no extra
# values needed. If it's ever empty, the WARNING path below is the signal.
kubectl wait --for=condition=Ready certificate/zitadel-tls -n "$NS" --timeout=600s || true
PAT=$(kubectl get secret iam-admin-pat -n "$NS" -o jsonpath='{.data.pat}' 2>/dev/null | base64 -d || true)
OP_SUB=""
if [ -n "$PAT" ]; then
  OP_SUB=$(curl -s -H "Authorization: Bearer $PAT" \
    "https://iam.${DOMAIN}/oidc/v1/userinfo" |
    python3 -c 'import sys,json; print(json.load(sys.stdin).get("sub",""))' 2>/dev/null || true)
fi
if [ -n "$OP_SUB" ]; then
  kubectl set env deploy/control-plane -n "$NS" "OPERATOR_SUBJECTS=$OP_SUB" >/dev/null
  echo "    seeded control-plane operator sub=$OP_SUB"
else
  echo "    WARNING: could not resolve an operator sub — set OPERATOR_SUBJECTS manually (ADR-0019)"
fi
kubectl rollout restart deploy/control-plane -n "$NS" >/dev/null
kubectl rollout status deploy/control-plane -n "$NS" --timeout=300s

echo "==> landing page (https://${LANDING_DOMAIN})"
# The HTML lives in the repo (landing/index.html); ship it as a ConfigMap so
# there is no image to build. external-dns publishes the record + cert-manager
# issues the cert from the ingress in manifests/landing.yaml.
kubectl create namespace landing --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl create configmap landing-html -n landing --from-file=index.html=../../landing/index.html \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
apply_tmpl manifests/landing.yaml
kubectl rollout status deploy/landing -n landing --timeout=120s

echo
# Report the honest state: several steps above are guarded (certs can lag DNS
# propagation), so confirm the platform cert actually issued before claiming up.
if kubectl get certificate/control-plane-tls -n "$NS" \
     -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null | grep -q True; then
  echo "ready: https://cp.${DOMAIN}   iam: https://iam.${DOMAIN}   landing: https://${LANDING_DOMAIN}"
else
  echo "PARTIAL: workloads are up but cp.${DOMAIN} has no cert yet."
  echo "  Check: DNS delegation for ${DOMAIN}, external-dns logs, and"
  echo "  'kubectl describe certificate control-plane-tls -n ${NS}'."
fi
