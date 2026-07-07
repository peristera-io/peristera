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
