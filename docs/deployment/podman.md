# Running the dev cluster on k3d + Podman

`hack/dev-cluster.sh` targets **k3d** and works with either **Docker** or
**Podman** as the container engine. The script auto-detects Podman and adjusts
its image import; this page documents the host setup Podman needs and the *why*
behind the differences — including which are inherent to Podman (so they apply on
any machine) and which were one-off machine state.

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

# 4. Run the deploy pointed at rootful Podman — completes end to end
PATH="$HOME/.cache/peristera-dev/bin:$PATH" \
DOCKER_HOST=unix:///run/podman/podman.sock \
  bash hack/dev-cluster.sh
```

Access when done (example dev URLs):

- Control plane — `http://cp.127.0.0.1.sslip.io:9080` (302 → IAM login is expected)
- IAM — `http://iam.127.0.0.1.sslip.io:9080`
- `kubectl` — `KUBECONFIG=~/.kube/config`, context `k3d-peristera-dev`

---

## Why Podman needs the two setup steps above

### Rootful socket + `docker` → Podman shim — *universal to Podman + k3d*

Not OS-specific. Two inherent Podman facts drive it:

- **Rootless vs rootful are separate image stores.** The `podman-docker` package
  ships `/usr/bin/docker` as a shim that runs `podman` **as the current user**
  (rootless). But k3d, told to use the **rootful** socket via `DOCKER_HOST`,
  reads the **rootful** store. So `docker build` (rootless) and `k3d image
  import` (rootful) would look at different storages — the build "succeeds" and
  the import finds nothing.
- **Fix:** the shim in step 3 makes `docker build` target the rootful service
  too (`podman --url unix:///run/podman/podman.sock`). Now build and import
  share one store.

This applies to any Podman host running this script. (Rootless-only setups would
also need Cilium to work rootless, which it generally doesn't here — hence
rootful.)

### How the script imports images on Podman — *handled automatically*

Podman names locally-built images with a `localhost/` prefix
(`localhost/peristera-stub:dev`), so `k3d image import` **by bare name** fails
with "no valid images specified". Because the script runs under `set -e`, an
unguarded import would abort the whole deploy right before it applies the Tenant
CRD, OpenFGA, and the control plane.

The script detects Podman (`docker version` reports it) and, instead of a
by-name import, **retags to the fully-qualified `docker.io/library/<name>`** the
manifests resolve to (they use `imagePullPolicy: IfNotPresent`, so a
correctly-named image already in containerd is enough) and **imports via
tarballs**, which bypasses the by-name runtime lookup. The Docker path is
unchanged. No manual step is required.

If you ever rebuild the four app images **outside** the script and need to load
them yourself, the same recipe works by hand:

```bash
export DOCKER_HOST=unix:///run/podman/podman.sock
TARD=$(mktemp -d)
for n in stub control-plane ergonomos kamara; do
  podman --url "$DOCKER_HOST" tag "localhost/peristera-$n:dev" "docker.io/library/peristera-$n:dev"
  podman --url "$DOCKER_HOST" save --format docker-archive -o "$TARD/$n.tar" "docker.io/library/peristera-$n:dev"
done
k3d image import -c peristera-dev "$TARD"/*.tar
rm -rf "$TARD"
```

## Machine-specific: a working `overlay` storage driver — *not Podman*

Rootful Podman needs a usable storage driver. It prefers the kernel `overlay`
driver; if the kernel can't provide it, `podman.service` fails to start with:

```text
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

## Notes / gotchas

- **Socket ACLs reset on reboot.** `/run` is tmpfs, so the two `setfacl` grants
  (step 2) must be re-applied after each boot. `podman.socket` itself stays
  enabled. (A durable alternative is a systemd drop-in setting `SocketGroup=` on
  `podman.socket` and adding your user to that group.)
- **Why not native k3s?** k3s embeds containerd and does not use Podman as its
  runtime; Podman would only build images, then side-load via
  `k3s ctr images import`, and the flannel→Cilium swap would be manual. k3d
  keeps the repo's tooling and CI path intact, so it's the recommended dev
  route on this machine.
