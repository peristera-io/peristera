# A reserved Flexible IP so the ingress address is stable across instance
# rebuilds — DNS (peristera.app in Scaleway DNS) points here once, permanently.
resource "scaleway_instance_ip" "node" {
  tags = ["peristera", "m7"]
}

# cloud-init installs k3s as a single-node cluster. Flannel and k3s's own
# network-policy controller are disabled so Cilium (installed by bootstrap.sh)
# owns both — the same CNI as dev (ADR-0016), which is what enforces
# cross-tenant NetworkPolicy. Traefik stays (k3s-bundled) as the ingress.
# Without a CNI the node is NotReady until Cilium lands; that's expected.
#
# metrics-server is disabled (as in dev, #42): under Cilium it can't scrape the
# kubelet (pod->node:10250), so it never gets endpoints, its metrics.k8s.io
# aggregated API stays unavailable, and k8s namespace GC — which blocks on
# aggregated-API discovery — stalls EVERY namespace deletion, wedging tenant
# off-boarding. Self-hosters wanting `kubectl top` must fix the kubelet-scrape
# path first.
locals {
  cloud_init = <<-CLOUD
    #cloud-config
    package_update: true
    runcmd:
      - curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--flannel-backend=none --disable-network-policy --disable=metrics-server --tls-san ${scaleway_instance_ip.node.address} --write-kubeconfig-mode 644" sh -
  CLOUD
}

resource "scaleway_instance_server" "node" {
  name              = var.name
  type              = var.instance_type
  image             = var.image
  ip_id             = scaleway_instance_ip.node.id
  security_group_id = scaleway_instance_security_group.node.id
  tags              = ["peristera", "m7"]

  # Scaleway injects the project's registered SSH keys (yours is present).
  user_data = {
    cloud-init = local.cloud_init
  }

  root_volume {
    size_in_gb = 40
  }
}
