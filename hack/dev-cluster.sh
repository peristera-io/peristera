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

echo "==> cluster"
if ! k3d cluster get "$CLUSTER" >/dev/null 2>&1; then
  k3d cluster create "$CLUSTER" \
    --port "9080:80@loadbalancer" --port "9443:443@loadbalancer" --wait
fi
# k3d writes the API server as 0.0.0.0, which macOS won't dial.
port=$(kubectl config view -o jsonpath="{.clusters[?(@.name==\"k3d-$CLUSTER\")].cluster.server}" | sed 's/.*://')
kubectl config set "clusters.k3d-$CLUSTER.server" "https://127.0.0.1:${port}" >/dev/null
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
docker build -q -t peristera-stub:dev iam/
docker build -q -t peristera-control-plane:dev control-plane/
k3d image import -c "$CLUSTER" peristera-stub:dev peristera-control-plane:dev
kubectl apply -f control-plane/deploy/crd/peristera.io_tenants.yaml >/dev/null
kubectl apply -f control-plane/deploy/manifests/control-plane.yaml >/dev/null
kubectl rollout restart deploy/control-plane -n "$NS" >/dev/null
kubectl rollout status deploy/control-plane -n "$NS" --timeout=300s

echo
echo "ready: http://cp.127.0.0.1.sslip.io:9080  (system key: $KEY_DIR/admin-client.key)"
echo "suite: cd control-plane && SYSTEM_USER_KEY=\$PWD/../$KEY_DIR/admin-client.key \\"
echo "       CP_BASE_URL=http://cp.127.0.0.1.sslip.io:9080 PERISTERA_E2E=1 \\"
echo "       go test -run TestFeatures -timeout 25m ."
