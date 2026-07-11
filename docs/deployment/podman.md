# Running the dev cluster on k3d + Podman

`hack/dev-cluster.sh` is written for **k3d + Docker** (it calls `docker build`
and `k3d image import`). It also runs on **k3d + Podman**, but three things need
handling. This page records them and — importantly — flags which are inherent to
Podman (so they apply on any machine) and which were one-off machine state.

> Scope: this is the *single-VM dev loop*, not the Scaleway path
> (`deploy/scaleway/`). It assumes **rootful** Podman, because Cilium (our CNI,
> ADR-0016) and the k3s nodes need privileged operations — eBPF program loading,
> cgroup and mount management — that rootless Podman can't grant across the user
> namespace.

## TL;DR

```bash
# 1. Rootful Podman API socket (once; survives reboot — it's socket-activated)
sudo systemctl enable --now podman.socket

# 2. Let your user reach the rootful socket (resets on reboot — see note)
sudo setfacl -m u:$USER:rx /run/podman
sudo setfacl -m u:$USER:rw /run/podman/podman.sock

# 3. A `docker` shim that routes into the SAME rootful storage k3d reads
mkdir -p ~/.cache/peristera-dev/bin
cat > ~/.cache/peristera-dev/bin/docker <<'EOF'
#!/usr/bin/env bash
exec podman --url unix:///run/podman/podman.sock "$@"
EOF
chmod +x ~/.cache/peristera-dev/bin/docker

# 4. Run the deploy pointed at rootful Podman
PATH="$HOME/.cache/peristera-dev/bin:$PATH" \
DOCKER_HOST=unix:///run/podman/podman.sock \
  bash hack/dev-cluster.sh
```

The script will still **abort at `k3d image import`** (see finding 3). When it
does, run the [image-import workaround](#3-k3d-image-import-fails-on-podman)
and then the [remaining tail](#finishing-the-script-after-the-import-abort)
manually. Everything before the import (Cilium, CNPG, Zitadel + Login) completes
unchanged.

Access when done:

- Control plane — <http://cp.127.0.0.1.sslip.io:9080> (302 → IAM login is expected)
- IAM — <http://iam.127.0.0.1.sslip.io:9080>
- `kubectl` — `KUBECONFIG=~/.kube/config`, context `k3d-peristera-dev`

---

## The three findings

### 1. Rootful socket + `docker` → Podman shim — *universal to Podman + k3d*

Not OS-specific. Two inherent Podman facts drive it:

- **Rootless vs rootful are separate image stores.** The `podman-docker` package
  ships `/usr/bin/docker` as a shim that runs `podman` **as the current user**
  (rootless). But k3d, told to use the **rootful** socket via `DOCKER_HOST`,
  reads the **rootful** store. So `docker build` (rootless) and `k3d image
  import` (rootful) look at different storages — the build "succeeds" and the
  import finds nothing.
- **Fix:** make `docker build` target the rootful service too, via a shim that
  calls `podman --url unix:///run/podman/podman.sock`. Now build and import
  share one store.

This applies to any Podman host running this script. (Rootless-only setups would
also need Cilium to work rootless, which it generally doesn't here — hence
rootful.)

### 2. A working `overlay` storage driver — *machine-specific, not Podman*

Rootful Podman needs a usable storage driver. It prefers the kernel `overlay`
driver; if the kernel can't provide it, `podman.service` fails to start with:

```
configure storage: kernel does not support overlay fs: 'overlay' is not
supported over extfs at "/var/lib/containers/storage/overlay"
```

This is **not** a Podman or distro property — it means the running kernel has no
overlayfs available. The case we hit: the `linux` package had been upgraded
(e.g. `7.0.3` → `7.1.3`) **without a reboot**, so the *running* kernel's module
tree (`/lib/modules/$(uname -r)`) had been deleted and `overlay` could no longer
be loaded. Diagnose and fix:

```bash
grep -w overlay /proc/filesystems        # empty  => overlay unavailable
ls -d /lib/modules/$(uname -r)           # missing => running kernel's modules gone
uname -r; pacman -Q linux                # do they match? if not, reboot
```

A fresh boot with matching modules never sees this. If a kernel genuinely lacks
overlay, install/enable `fuse-overlayfs` and point Podman's `storage.conf`
`mount_program` at it — but note the containerd **inside** the k3s node needs a
working snapshotter too, so a truly overlay-less kernel is the wrong host for
this stack.

### 3. `k3d image import` fails on Podman — *universal to Podman + k3d*

Podman names locally-built images with a `localhost/` prefix
(`localhost/peristera-stub:dev`). The script imports them by bare name:

```
k3d image import -c peristera-dev peristera-stub:dev ...
# WARN  Image 'peristera-stub:dev' ... couldn't be found in the container runtime
# ERRO  Failed to import image(s): no valid images specified
```

Because the script runs under `set -e`, this **aborts the deploy** right before
it applies the Tenant CRD, OpenFGA, and the control plane — so on a first run
everything up to Zitadel is present and nothing after it is.

**Workaround** — retag to the fully-qualified name the manifests expect
(`docker.io/library/…`; manifests use `imagePullPolicy: IfNotPresent`, so a
correctly-named image already in containerd is enough), save to tars, and import
the **tars** (tar import bypasses the by-name runtime lookup that fails):

```bash
export DOCKER_HOST=unix:///run/podman/podman.sock
TARD=~/.cache/peristera-dev/imgtars; mkdir -p "$TARD"
for n in stub control-plane ergonomos kamara; do
  podman --url "$DOCKER_HOST" tag "localhost/peristera-$n:dev" "docker.io/library/peristera-$n:dev"
  podman --url "$DOCKER_HOST" save --format docker-archive -o "$TARD/$n.tar" "docker.io/library/peristera-$n:dev"
done
k3d image import -c peristera-dev "$TARD"/stub.tar "$TARD"/control-plane.tar \
  "$TARD"/ergonomos.tar "$TARD"/kamara.tar
```

## Finishing the script after the import abort

The steps the aborted script never reached (`hack/dev-cluster.sh` tail):

```bash
export KUBECONFIG="$HOME/.kube/config"
NS=peristera-system
kubectl apply -f control-plane/deploy/crd/peristera.io_tenants.yaml
kubectl apply -f control-plane/deploy/manifests/cp-openfga.yaml
kubectl apply -f control-plane/deploy/manifests/control-plane.yaml
kubectl rollout status deploy/cp-openfga -n "$NS" --timeout=120s

# Seed the control-plane operator (ADR-0019): resolve the iam-admin machine
# user's `sub` from its PAT and inject it as OPERATOR_SUBJECTS.
PAT=$(kubectl get secret iam-admin-pat -n "$NS" -o jsonpath='{.data.pat}' | base64 -d)
OP_SUB=$(curl -s -H "Authorization: Bearer $PAT" \
  http://iam.127.0.0.1.sslip.io:9080/oidc/v1/userinfo \
  | python3 -c 'import sys,json; print(json.load(sys.stdin)["sub"])')
kubectl set env deploy/control-plane -n "$NS" "OPERATOR_SUBJECTS=$OP_SUB"

kubectl rollout restart deploy/control-plane -n "$NS"
kubectl rollout status deploy/control-plane -n "$NS" --timeout=300s
```

Verify:

```bash
kubectl get pods -n peristera-system      # control-plane, cp-openfga, zitadel* all Running
curl -s -o /dev/null -w '%{http_code}\n' http://cp.127.0.0.1.sslip.io:9080/   # 302
```

## Notes / gotchas

- **Socket ACLs reset on reboot.** `/run` is tmpfs, so the two `setfacl` grants
  (step 2) must be re-applied after each boot. `podman.socket` itself stays
  enabled. (A durable alternative is a systemd drop-in setting `SocketGroup=` on
  `podman.socket` and adding your user to that group.)
- **Rebuilds hit finding 3 again.** Any time you rebuild the four app images,
  the plain script won't get past `k3d image import` on Podman — re-run the
  retag/save/tar-import block.
- **Why not native k3s?** k3s embeds containerd and does not use Podman as its
  runtime; Podman would only build images, then side-load via
  `k3s ctr images import`, and the flannel→Cilium swap would be manual. k3d
  keeps the repo's tooling and CI path intact, so it's the recommended dev
  route on this machine.
```
