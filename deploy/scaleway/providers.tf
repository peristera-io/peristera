# Credentials come from the environment — `source ../../.env.scaleway` first:
#   SCW_ACCESS_KEY, SCW_SECRET_KEY, SCW_DEFAULT_PROJECT_ID, SCW_DEFAULT_ORGANIZATION_ID
# Nothing sensitive lives in this repo; Tofu state (Object Storage backend for
# real deploys) and platform secrets (Scaleway Secret Manager) stay out of git.
provider "scaleway" {
  region = var.region
  zone   = var.zone
}
