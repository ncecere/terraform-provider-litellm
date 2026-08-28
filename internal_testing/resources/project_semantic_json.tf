# LiteLLM Project management requires an Enterprise license.
resource "litellm_project" "semantic_json" {
  team_id       = litellm_team.minimal.id
  project_alias = "project-semantic-json-lifecycle"

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

  tags = ["semantic-json"]

  model_rpm_limit = {
    "gpt-4o-mini" = 100
  }

  model_tpm_limit = {
    "gpt-4o-mini" = 10000
  }
}

output "project_semantic_json_id" {
  value = litellm_project.semantic_json.id
}
