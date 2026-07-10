locals {
  project_id     = "project-d60a83c1-2c60-4d51-ad0"
  project_number = "756865700041"
  region         = "us-central1"
  # The only GitHub repo whose Actions can impersonate the deploy SA.
  github_repo = "dperez4787/cosmo-router"
}

provider "google" {
  project = local.project_id
  region  = local.region
}
