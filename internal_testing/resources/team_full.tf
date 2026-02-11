# litellm_team - Full
# All attributes populated

resource "litellm_team" "full" {
  team_alias      = "test-team-full"
  max_budget      = 500.0
  tpm_limit       = 100000
  rpm_limit       = 1000
  tpm_limit_type  = "guaranteed_throughput"
  rpm_limit_type  = "guaranteed_throughput"
  budget_duration = "30d"
  blocked         = false

  models = ["gpt-4o", "gpt-4o-mini"]
  # tags requires LiteLLM Enterprise license
  # tags = ["testing", "full"]
  guardrails = []
  prompts    = []

  team_member_permissions = []
  team_member_budget      = 50.0
  team_member_rpm_limit   = 100
  team_member_tpm_limit   = 10000

  metadata = {
    "environment" = "testing"
  }

  model_aliases = {
    "fast" = "gpt-4o-mini"
  }

  model_rpm_limit = {
    "gpt-4o" = 500
  }

  model_tpm_limit = {
    "gpt-4o" = 50000
  }
}

output "team_full_id" {
  value = litellm_team.full.id
}
