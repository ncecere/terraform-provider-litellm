resource "litellm_team" "structured_metadata" {
  team_alias = "structured-metadata"

  metadata = {
    owner = "platform"
  }

  metadata_json = jsonencode({
    deployment = {
      production = true
      revision   = 9007199254740993
      owner      = null
      regions    = ["us-east", "us-west"]
      options    = {}
    }
  })

  tags = ["production", "platform"]

  model_rpm_limit = {
    "gpt-4o-mini" = 100
  }
}
