# litellm_tag - Full
# All attributes populated

resource "litellm_tag" "full" {
  name                  = "test-tag-full"
  description           = "A full test tag with all attributes"
  models                = ["gpt-4o", "gpt-4o-mini"]
  max_budget            = 500.0
  soft_budget           = 400.0
  max_parallel_requests = 10
  tpm_limit             = 50000
  rpm_limit             = 500
  budget_duration       = "30d"
  model_max_budget = jsonencode({
    "gpt-4o" = 250.0
  })
}

output "tag_full_id" {
  value = litellm_tag.full.id
}
