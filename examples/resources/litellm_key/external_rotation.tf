# The external owner must already publish this key's raw token in AWSCURRENT.
# This configuration adopts it; it does not generate a key or create the secret.
resource "litellm_key" "externally_rotated" {
  service_account_id = "sa-example"
  key_alias          = "key-example"
  team_id            = "example-team"
  project_id         = "example-project"

  external_rotation = {
    secret_arn = "arn:aws:secretsmanager:us-west-2:123456789012:secret:service-accounts/key-example-AbCdEf"
  }

  models          = ["example-model"]
  max_budget      = 100
  budget_duration = "30d"
}
