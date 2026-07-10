output "wif_provider" {
  description = "Value for the WIF_PROVIDER GitHub secret"
  value       = google_iam_workload_identity_pool_provider.github_cosmorouter.name
}

output "deploy_sa" {
  description = "Value for the DEPLOY_SA GitHub secret"
  value       = google_service_account.deploy.email
}
