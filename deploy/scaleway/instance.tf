# A reserved Flexible IP so the ingress address is stable across instance
# rebuilds — DNS (peristera.app in Scaleway DNS) points here once, permanently.
resource "scaleway_instance_ip" "node" {
  tags = ["peristera", "spike"]
}

# cloud-init installs k3s as a single-node cluster. Vanilla k3s (flannel +
# bundled Traefik) is enough to prove the domain/cert/https trio; the Cilium
# swap (for cross-tenant NetworkPolicy enforcement, as in dev) comes when we
# deploy real multi-tenant isolation.
locals {
  cloud_init = <<-CLOUD
    #cloud-config
    package_update: true
    runcmd:
      - curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="--tls-san ${scaleway_instance_ip.node.address} --write-kubeconfig-mode 644" sh -
  CLOUD
}

resource "scaleway_instance_server" "node" {
  name  = var.name
  type  = var.instance_type
  image = var.image
  ip_id = scaleway_instance_ip.node.id
  tags  = ["peristera", "spike"]

  # Scaleway injects the project's registered SSH keys (yours is present).
  user_data = {
    cloud-init = local.cloud_init
  }

  root_volume {
    size_in_gb = 40
  }
}
