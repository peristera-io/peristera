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

  # Versioning is the tamper/overwrite backstop for the interim blob backup +
  # secret escrow (#59/#77): an attacker with the in-cluster S3 key who
  # rewrites chunks or the nightly escrow object can at worst add a bad
  # NEWEST version — the prior good version stays recoverable. Overhead is
  # ~zero in the good case: the escrow is kilobytes/night, and chunk objects
  # never legitimately change (content-addressed; the job uploads with
  # --immutable), so noncurrent versions only appear under tampering.
  versioning {
    enabled = true
  }
}
