variable "organization_id" {
  description = "Stable caller-selected organization ID used for semantic metadata recovery."
  type        = string
}

resource "litellm_organization" "structured_metadata" {
  organization_id    = var.organization_id
  organization_alias = "structured-metadata"

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

  model_rpm_limit = {
    "gpt-4o-mini" = 100
  }
}
