# LiteLLM Project management requires an Enterprise license.
resource "litellm_project" "full" {
  project_alias         = "test-project-full"
  description           = "Full nested-budget lifecycle fixture"
  team_id               = litellm_team.full.id
  models                = ["gpt-4o", "gpt-4o-mini"]
  tags                  = ["testing", "full"]
  max_budget            = 500.0
  soft_budget           = 400.0
  budget_duration       = "30d"
  tpm_limit             = 100000
  rpm_limit             = 1000
  max_parallel_requests = 25
  blocked               = false

  metadata = {
    environment = "testing"
  }

  model_rpm_limit = {
    "gpt-4o" = 500
  }

  model_tpm_limit = {
    "gpt-4o" = 50000
  }
}

output "project_full_id" {
  value = litellm_project.full.id
}
