# Two-SA split, mirroring imdb-federation: the deploy SA (impersonated by
# GitHub Actions via WIF) pushes images and deploys; the runtime SA is the
# identity of the running router. The router reads no secrets today — the
# execution config is baked into the image — so the runtime SA has no grants.
# The future policy-service integration will add its grants here.

resource "google_service_account" "deploy" {
  account_id   = "cosmorouter-deploy"
  display_name = "cosmo-router GitHub Actions deploy"
}

resource "google_service_account" "runtime" {
  account_id   = "cosmorouter-run"
  display_name = "cosmo-router Cloud Run runtime"
}

resource "google_project_iam_member" "deploy_ar_writer" {
  project = local.project_id
  role    = "roles/artifactregistry.writer"
  member  = "serviceAccount:${google_service_account.deploy.email}"
}

resource "google_project_iam_member" "deploy_run_admin" {
  project = local.project_id
  role    = "roles/run.admin"
  member  = "serviceAccount:${google_service_account.deploy.email}"
}

# actAs on the runtime SA only (not project-wide serviceAccountUser)
resource "google_service_account_iam_member" "deploy_act_as_runtime" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deploy.email}"
}

# GitHub Actions from this repo (and only this repo) may impersonate the deploy SA.
resource "google_service_account_iam_member" "deploy_wif" {
  service_account_id = google_service_account.deploy.name
  role               = "roles/iam.workloadIdentityUser"
  member  = "principalSet://iam.googleapis.com/projects/${local.project_number}/locations/global/workloadIdentityPools/github-pool/attribute.repository/${local.github_repo}"
}
