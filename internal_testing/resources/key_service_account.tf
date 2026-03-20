# litellm_key - Service Account
#
# Regression test for: https://github.com/ncecere/terraform-provider-litellm/issues/76
#
# Verifies that creating a key with service_account_id but WITHOUT an explicit
# key_alias does NOT raise:
#   "Provider produced inconsistent result after apply: .key_alias was null,
#    but now cty.StringVal(...)"
#
# The provider must automatically default key_alias to the service_account_id
# value (both in the API request and by reading it back as a Computed field),
# so Terraform never sees a null→value mismatch after apply.
#
# Note: the LiteLLM API requires team_id for service account keys, so a team
# is created inline to keep this fixture self-contained.

resource "litellm_team" "service_account_team" {
  team_alias = "test-team-service-account"
}

resource "litellm_key" "service_account" {
  service_account_id = "github-ci"
  team_id            = litellm_team.service_account_team.id
  # key_alias is intentionally omitted — the provider must default it to
  # "github-ci" without requiring the caller to set it explicitly.
}

output "key_service_account_id" {
  value = litellm_key.service_account.id
}

output "key_service_account_key" {
  value     = litellm_key.service_account.key
  sensitive = true
}

# This output must equal "github-ci" after apply — confirming the provider
# defaulted key_alias from service_account_id and stored it in state.
output "key_service_account_alias" {
  value       = litellm_key.service_account.key_alias
  description = "Must equal 'github-ci' — automatically defaulted from service_account_id."
}
