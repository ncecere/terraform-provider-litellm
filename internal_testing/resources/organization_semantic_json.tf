# litellm_organization - lossless heterogeneous metadata
# Caller-selected identity makes an accepted create recoverable.
resource "litellm_organization" "semantic_json" {
  organization_id    = "org-semantic-json-lifecycle-20260828"
  organization_alias = "semantic-json-lifecycle"

  metadata = {
    legacy_owner = "terraform"
  }

  metadata_json = jsonencode({
    deployment = {
      production = true
      revision   = 9007199254740993
      owner      = null
      regions    = ["us-east", "us-west"]
      empty      = {}
    }
  })

  model_rpm_limit = {
    "gpt-4o-mini" = 100
  }

  model_tpm_limit = {
    "gpt-4o-mini" = 10000
  }
}

output "organization_semantic_json_id" {
  value = litellm_organization.semantic_json.id
}
