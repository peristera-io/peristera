terraform {
  required_version = ">= 1.6"
  required_providers {
    scaleway = { source = "scaleway/scaleway", version = "~> 2.53" }
    random   = { source = "hashicorp/random", version = "~> 3.6" }
    # tls generates the admin-client system-user keypair (Zitadel System API
    # auth). The private key is born inside Tofu and pushed straight into
    # Scaleway Secret Manager — it never lands on disk or in git.
    tls = { source = "hashicorp/tls", version = "~> 4.0" }
  }
}
