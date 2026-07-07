output "node_ip" {
  description = "The stable public IP — point *.peristera.app / DNS here"
  value       = scaleway_instance_ip.node.address
}
output "ssh" {
  value = "ssh root@${scaleway_instance_ip.node.address}"
}
output "kubeconfig_hint" {
  value = "scp root@${scaleway_instance_ip.node.address}:/etc/rancher/k3s/k3s.yaml - | sed 's/127.0.0.1/${scaleway_instance_ip.node.address}/' > /tmp/spike.kubeconfig"
}
output "blobs_bucket"   { value = scaleway_object_bucket.blobs.name }
output "backups_bucket" { value = scaleway_object_bucket.backups.name }
