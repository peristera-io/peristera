# Object Storage: tenant blobs (S3 backend for Kamara, deferred #21) and
# backups (CNPG dumps + blob lifecycle). Names are globally unique, so suffix.
resource "random_id" "bucket" {
  byte_length = 3
}

resource "scaleway_object_bucket" "blobs" {
  name = "${var.name}-blobs-${random_id.bucket.hex}"
  tags = { app = "peristera", role = "blobs" }
}

resource "scaleway_object_bucket" "backups" {
  name = "${var.name}-backups-${random_id.bucket.hex}"
  tags = { app = "peristera", role = "backups" }
}
