#!/usr/bin/env bash
# One command from zero to a running Peristera dev environment on k3d:
# CNPG, Zitadel + Login v2, in-cluster DNS for the sslip domains, and the
# control plane (controller + UI/API) — everything the godog suite needs.
# Idempotent: safe to re-run on an existing cluster. Used by CI (e2e job)
# and humans alike; the per-component details live in iam/README.md and
# control-plane/README.md.
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER=${CLUSTER:-peristera-dev}
KEY_DIR=${KEY_DIR:-.dev-secrets}
NS=peristera-system
CILIUM_VERSION=${CILIUM_VERSION:-1.19.4}

echo "==> cluster"
if ! k3d cluster get "$CLUSTER" >/dev/null 2>&1; then
  # Cilium is the CNI (ADR-0016) so it can enforce NetworkPolicy — k3s's
  # bundled flannel does not. Disable flannel and k3s's own network-policy
  # controller so Cilium owns both. No --wait: without a CNI the node stays
  # NotReady until Cilium is installed two steps down (so --wait would hang
  # and roll the cluster back).
  k3d cluster create "$CLUSTER" \
    --port "9080:80@loadbalancer" --port "9443:443@loadbalancer" \
    --k3s-arg "--flannel-backend=none@server:*" \
    --k3s-arg "--disable-network-policy@server:*" \
    --k3s-arg "--disable=metrics-server@server:*"
  # metrics-server is disabled: the platform doesn't use it, and under
  # Cilium on k3d it cannot scrape the kubelet (pod->node:10250), so it
  # never becomes Ready — which keeps the metrics.k8s.io APIService down and
  # stalls *every* namespace deletion (k8s's namespace GC blocks on aggregated
  # -API discovery). That broke tenant off-boarding. Self-hosters who want
  # `kubectl top` should fix the kubelet-scrape path first (issue tracked).
fi
# k3d writes the API server as 0.0.0.0, which macOS won't dial.
port=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"k3d-$CLUSTER\")].cluster.server}" | sed 's/.*://')
kubectl config set "clusters.k3d-$CLUSTER.server" "https://127.0.0.1:${port}" >/dev/null

echo "==> cilium CNI"
helm repo add cilium https://helm.cilium.io/ >/dev/null 2>&1 || true
helm repo update cilium >/dev/null
# kube-proxy coexistence: k3s ships an embedded kube-proxy, so Cilium must
# NOT replace it (kubeProxyReplacement=false) or the agent can't reach the
# API server. Installed via helm, not the cilium CLI: on k3d/macOS the CLI
# bakes the host-side kubeconfig API address (127.0.0.1:<hostport>) into the
# pods, which is unreachable from inside the cluster.
helm upgrade --install cilium cilium/cilium --version "$CILIUM_VERSION" -n kube-system \
  --set kubeProxyReplacement=false --set operator.replicas=1 --wait --timeout 5m
kubectl wait --for=condition=Ready node --all --timeout=300s >/dev/null

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "==> cloudnative-pg"
helm repo add cnpg https://cloudnative-pg.github.io/charts >/dev/null 2>&1 || true
helm repo add zitadel https://charts.zitadel.com >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install cnpg cnpg/cloudnative-pg -n cnpg-system --create-namespace --wait

echo "==> zitadel database"
kubectl apply -f iam/deploy/dev/cnpg-zitadel.yaml
kubectl wait --for=condition=Ready cluster/zitadel-db -n "$NS" --timeout=300s
if ! kubectl get secret zitadel-db-dsn -n "$NS" >/dev/null 2>&1; then
  PW=$(kubectl get secret zitadel-db-app -n "$NS" -o jsonpath='{.data.password}' | base64 -d)
  kubectl create secret generic zitadel-db-dsn -n "$NS" \
    --from-literal=dsn="postgresql://zitadel:${PW}@zitadel-db-rw.${NS}.svc.cluster.local:5432/zitadel?sslmode=require"
fi

echo "==> system-user keypair"
mkdir -p "$KEY_DIR"
if [ ! -f "$KEY_DIR/admin-client.key" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 365 -subj "/CN=admin-client" \
    -keyout "$KEY_DIR/admin-client.key" -out "$KEY_DIR/admin-client.crt"
fi
kubectl create secret generic admin-client-tls -n "$NS" \
  --from-file=tls.crt="$KEY_DIR/admin-client.crt" \
  --from-file=tls.key="$KEY_DIR/admin-client.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

echo "==> zitadel + login v2"
helm upgrade --install zitadel zitadel/zitadel -n "$NS" \
  -f iam/deploy/dev/zitadel-values.yaml --timeout 10m >/dev/null
kubectl rollout status deploy/zitadel -n "$NS" --timeout=600s
kubectl rollout status deploy/zitadel-login -n "$NS" --timeout=300s

echo "==> in-cluster DNS for sslip domains + traefik :9080"
TRAEFIK_IP=$(kubectl get svc traefik -n kube-system -o jsonpath='{.spec.clusterIP}')
sed "s/TRAEFIK_IP/$TRAEFIK_IP/" iam/deploy/dev/coredns-sslip.yaml | kubectl apply -f - >/dev/null
if ! kubectl get svc traefik -n kube-system -o jsonpath='{.spec.ports[*].port}' | grep -qw 9080; then
  kubectl patch svc traefik -n kube-system --type=json \
    -p '[{"op":"add","path":"/spec/ports/-","value":{"name":"web-9080","port":9080,"protocol":"TCP","targetPort":"web"}}]'
fi
kubectl rollout restart deploy/coredns -n kube-system >/dev/null
kubectl rollout status deploy/coredns -n kube-system --timeout=120s

echo "==> images + control plane"
# Repo-root context: images depend on the sibling lib/ module.
docker build -q -f iam/Dockerfile -t peristera-stub:dev .
docker build -q -f control-plane/Dockerfile -t peristera-control-plane:dev .
docker build -q -f ergonomos/Dockerfile -t peristera-ergonomos:dev .
docker build -q -f kamara/Dockerfile -t peristera-kamara:dev .
IMAGES="peristera-stub peristera-control-plane peristera-ergonomos peristera-kamara"
if docker version 2>/dev/null | grep -qiE 'podman'; then
  # Podman names local builds `localhost/<name>`, so `k3d image import` by the
  # bare name fails ("no valid images specified"). Retag to the fully qualified
  # docker.io/library/<name> the manifests resolve to (imagePullPolicy:
  # IfNotPresent), then import via tarballs — which sidesteps the by-name
  # runtime lookup that breaks on Podman. See docs/deployment/podman.md.
  TARD=$(mktemp -d)
  tars=()
  for n in $IMAGES; do
    docker tag "$n:dev" "docker.io/library/$n:dev"
    docker save --format docker-archive -o "$TARD/$n.tar" "docker.io/library/$n:dev"
    tars+=("$TARD/$n.tar")
  done
  k3d image import -c "$CLUSTER" "${tars[@]}"
  rm -rf "$TARD"
else
  k3d image import -c "$CLUSTER" $(for n in $IMAGES; do printf '%s:dev ' "$n"; done)
fi
kubectl apply -f control-plane/deploy/crd/peristera.io_tenants.yaml >/dev/null
kubectl apply -f control-plane/deploy/manifests/cp-openfga.yaml >/dev/null
kubectl apply -f control-plane/deploy/manifests/control-plane.yaml >/dev/null

# Seed the control-plane operator (ADR-0019): the dev operator is the Zitadel
# chart's iam-admin machine user; resolve its `sub` from its PAT via userinfo
# and inject it as OPERATOR_SUBJECTS, so the control plane authorizes it.
# Without a seed, every operator request is denied 403.
kubectl rollout status deploy/cp-openfga -n "$NS" --timeout=120s
PAT=$(kubectl get secret iam-admin-pat -n "$NS" -o jsonpath='{.data.pat}' 2>/dev/null | base64 -d || true)
OP_SUB=""
if [ -n "$PAT" ]; then
  OP_SUB=$(curl -s -H "Authorization: Bearer $PAT" \
    http://iam.127.0.0.1.sslip.io:9080/oidc/v1/userinfo |
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

echo
echo "ready: http://cp.127.0.0.1.sslip.io:9080  (system key: $KEY_DIR/admin-client.key)"
echo "suite: cd control-plane && SYSTEM_USER_KEY=\$PWD/../$KEY_DIR/admin-client.key \\"
echo "       CP_BASE_URL=http://cp.127.0.0.1.sslip.io:9080 PERISTERA_E2E=1 \\"
echo "       go test -run TestFeatures -timeout 25m ."
