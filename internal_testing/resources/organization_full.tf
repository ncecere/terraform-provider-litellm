# litellm_organization - Full
# All attributes populated

resource "litellm_organization" "full" {
  organization_alias    = "test-org-full"
  max_budget            = 1000.0
  soft_budget           = 800.0
  tpm_limit             = 200000
  rpm_limit             = 2000
  max_parallel_requests = 50
  budget_duration       = "30d"

  models = ["gpt-4o", "gpt-4o-mini"]

  metadata = {
    "environment" = "testing"
  }

  model_rpm_limit = {
    "gpt-4o" = 1000
  }

  model_tpm_limit = {
    "gpt-4o" = 100000
  }
}

output "org_full_id" {
  value = litellm_organization.full.id
}

output "org_full_created_at" {
  value = litellm_organization.full.created_at
}
