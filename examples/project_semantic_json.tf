variable "project_team_id" {
  description = "Parent team ID for the project."
  type        = string
}

resource "litellm_project" "structured_metadata" {
  team_id       = var.project_team_id
  project_alias = "structured-metadata"

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
