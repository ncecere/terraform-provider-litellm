# litellm_key - Full
# All attributes populated, including a key value with URL-special characters
# ('#', '!') to guard against the regression where '#' was interpreted as a
# URL fragment delimiter in /key/info?key=..., silently truncating the value
# and causing a 404 on read-back after creation.

resource "litellm_key" "full" {
  key                   = "sk-smoke-test#special!chars"
  key_alias             = "test-key-full"
  models                = ["gpt-4o", "gpt-4o-mini"]
  max_budget            = 100.0
  tpm_limit             = 50000
  rpm_limit             = 200
  tpm_limit_type        = "best_effort_throughput"
  rpm_limit_type        = "best_effort_throughput"
  budget_duration       = "30d"
  max_parallel_requests = 10
  soft_budget           = 80.0
  duration              = "90d"
  blocked               = false

  allowed_routes             = ["llm_api_routes"]
  allowed_passthrough_routes = []
  allowed_cache_controls     = ["no-cache"]
  guardrails                 = []
  prompts                    = []
  enforced_params            = []
  # tags requires LiteLLM Enterprise license
  # tags = ["testing", "full"]

  metadata = {
    "environment" = "testing"
    "owner"       = "terraform"
  }

  aliases = {}

  config = {}

  permissions = {}

  # model_max_budget requires LiteLLM Enterprise license
  # model_max_budget = {
  #   "gpt-4o" = 50.0
  # }

  model_rpm_limit = {
    "gpt-4o" = 100
  }

  model_tpm_limit = {
    "gpt-4o" = 25000
  }
}

output "key_full_id" {
  value = litellm_key.full.id
}
