resource "google_artifact_registry_repository" "cosmo_router" {
  repository_id = "cosmo-router"
  location      = local.region
  format        = "DOCKER"
  description   = "Custom Cosmo router images for the IMDb GraphQL federation"

  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }

  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      older_than = "2592000s" # 30 days
    }
  }
}
