variable "region" {
  description = "Scaleway region"
  type        = string
  default     = "fr-par"
}
variable "zone" {
  description = "Scaleway zone"
  type        = string
  default     = "fr-par-1"
}
variable "name" {
  description = "Name prefix for spike resources"
  type        = string
  default     = "peristera-spike"
}
variable "instance_type" {
  # PLAY2-MICRO = 4 vCPU / 8 GB, ~EUR 0.055/hr (~EUR 40/mo if left running).
  # The spike creates -> verifies -> destroys, so it costs cents. PLAY2-NANO
  # (4 GB, ~EUR 20/mo) is too tight for the full stack + Collabora.
  description = "Scaleway instance type for the single-node k3s host"
  type        = string
  default     = "PLAY2-MICRO"
}
variable "image" {
  description = "Base image"
  type        = string
  default     = "ubuntu_jammy"
}

variable "domain" {
  # Where tenants run (model A): <app>.<tenant>.peristera.app. The platform
  # itself is cp.<domain> (control plane) and iam.<domain> (Zitadel). This
  # zone must be delegated to Scaleway DNS so external-dns can write records.
  description = "Public base domain, delegated to Scaleway DNS"
  type        = string
  default     = "peristera.app"
}

variable "letsencrypt_email" {
  # The ACME account contact. Empty here on purpose — set it in the
  # environment (TF_VAR_letsencrypt_email) so no personal address lands in
  # the public repo. cert-manager needs it for the Let's Encrypt account.
  description = "Contact email for the Let's Encrypt ACME account"
  type        = string
  default     = ""
}

variable "admin_cidr" {
  # SSH (22) and the k3s API (6443) are exposed only to this CIDR. Scaleway
  # opens 6443 to the whole internet by default, so this is load-bearing:
  # set it to your /32 (TF_VAR_admin_cidr) before apply. The default is a
  # deliberately unusable placeholder that documents the intent and fails
  # closed if someone forgets — it locks admin access to a single TEST-NET-3
  # address (RFC 5737) that reaches nothing.
  description = "CIDR allowed to reach SSH (22) and the k3s API (6443)"
  type        = string
  default     = "203.0.113.1/32"
}
