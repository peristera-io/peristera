output "node_ip" {
  description = "The stable public IP — point *.<domain> DNS here"
  value       = scaleway_instance_ip.node.address
}
output "ssh" {
  value = "ssh root@${scaleway_instance_ip.node.address}"
}
output "kubeconfig_hint" {
  value = "scp root@${scaleway_instance_ip.node.address}:/etc/rancher/k3s/k3s.yaml - | sed 's/127.0.0.1/${scaleway_instance_ip.node.address}/' > /tmp/peristera.kubeconfig"
}
output "blobs_bucket" { value = scaleway_object_bucket.blobs.name }
output "backups_bucket" { value = scaleway_object_bucket.backups.name }

output "domain" {
  description = "Public base domain (bootstrap.sh reads this)"
  value       = var.domain
}
output "cp_url" { value = "https://cp.${var.domain}" }
output "iam_url" { value = "https://iam.${var.domain}" }

output "platform_secret_names" {
  description = "Secret Manager entries ESO syncs into the cluster"
  value       = keys(local.platform_secrets)
}
