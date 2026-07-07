# The node's firewall. Scaleway's default security group leaves the k3s API
# (6443) open to the whole internet, which is the single biggest exposure of a
# public single-node cluster — an unauthenticated attacker probing the API
# server. So we drop inbound by default and open only:
#   - 80/443 to the world (Traefik ingress: the whole point of the deploy)
#   - 22 (SSH) + 6443 (k3s API) to var.admin_cidr only
# Outbound stays open (pulling images from ghcr, ACME to Let's Encrypt, DNS
# and Secret Manager to Scaleway APIs).
resource "scaleway_instance_security_group" "node" {
  name                    = "${var.name}-fw"
  description             = "Peristera node: web open, admin locked to admin_cidr"
  inbound_default_policy  = "drop"
  outbound_default_policy = "accept"
  tags                    = ["peristera", "m7"]

  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 80
  }
  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 443
  }
  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 22
    ip_range = var.admin_cidr
  }
  inbound_rule {
    action   = "accept"
    protocol = "TCP"
    port     = 6443
    ip_range = var.admin_cidr
  }
}
