resource "litellm_team" "semantic_json" {
  team_alias = "team-semantic-json-lifecycle"

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

output "team_semantic_json_id" {
  value = litellm_team.semantic_json.id
}
