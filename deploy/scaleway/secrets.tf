# Platform secrets live in Scaleway Secret Manager, generated here so no
# sensitive value ever touches git or a developer's disk. The External Secrets
# Operator (bootstrap.sh) syncs them into the cluster as native Secrets. The
# chicken-and-egg root — the Scaleway API credentials ESO itself needs to read
# Secret Manager — is the ONE secret injected directly at bootstrap, not stored
# here.

# Zitadel's masterkey encrypts its secrets at rest. Zitadel requires exactly
# 32 characters; alphanumeric keeps it shell/env-safe.
resource "random_password" "zitadel_masterkey" {
  length  = 32
  special = false
}

# The preshared bearer token the control plane uses to talk to the platform
# OpenFGA (operator authZ, ADR-0019).
resource "random_password" "cp_openfga_token" {
  length  = 40
  special = false
}

# The admin-client system-user keypair: Zitadel authenticates the control
# plane's System API calls (create/delete tenant virtual instances) against
# this self-signed cert (RS256 JWT, ADR-0006). Born in Tofu, stored in Secret
# Manager, never written to disk.
resource "tls_private_key" "admin_client" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "admin_client" {
  private_key_pem = tls_private_key.admin_client.private_key_pem
  subject {
    common_name = "admin-client"
  }
  validity_period_hours = 26280 # 3 years
  allowed_uses          = ["key_encipherment", "digital_signature", "client_auth"]
}

locals {
  # Every platform secret as name => value, fanned out into Secret Manager
  # below. The bootstrap ExternalSecrets reference these exact names.
  platform_secrets = {
    "peristera-zitadel-masterkey" = random_password.zitadel_masterkey.result
    "peristera-cp-openfga-token"  = random_password.cp_openfga_token.result
    "peristera-admin-client-crt"  = tls_self_signed_cert.admin_client.cert_pem
    # PKCS#8 ("BEGIN PRIVATE KEY") — the control plane parses the system-user
    # key with x509.ParsePKCS8PrivateKey (dev's openssl key is PKCS#8 too).
    # private_key_pem is PKCS#1 for RSA ("BEGIN RSA PRIVATE KEY"), which fails
    # to parse. Same key material, so the already-issued cert stays valid.
    "peristera-admin-client-key" = tls_private_key.admin_client.private_key_pem_pkcs8
  }
}

resource "scaleway_secret" "platform" {
  for_each    = local.platform_secrets
  name        = each.key
  description = "Peristera platform secret (synced into k3s by ESO)"
  tags        = ["peristera", "m7"]
}

resource "scaleway_secret_version" "platform" {
  for_each  = local.platform_secrets
  secret_id = scaleway_secret.platform[each.key].id
  data      = each.value
}
