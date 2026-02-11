# litellm_model - Full
# All attributes populated

resource "litellm_model" "full" {
  model_name          = "test-model-full"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o"

  tpm = 100000
  rpm = 500

  model_api_key  = "sk-fake-key-for-testing"
  model_api_base = "https://api.openai.com/v1"

  tier = "paid"
  mode = "chat"

  input_cost_per_million_tokens  = 2.50
  output_cost_per_million_tokens = 10.00

  reasoning_effort                   = "medium"
  thinking_enabled                   = true
  thinking_budget_tokens             = 2048
  merge_reasoning_content_in_choices = true

  access_groups = ["test-access-group"]

  additional_litellm_params = {
    "max_tokens"  = "4096"
    "temperature" = "0.7"
  }
}

output "model_full_id" {
  value = litellm_model.full.id
}
