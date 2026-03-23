# litellm_model - Wildcard routing
# Verifies that provider-specific wildcard routing (openai/*) applies without
# errors. On first apply the API returns no "mode" value; the provider must
# resolve the Computed mode attribute to null rather than leaving it unknown,
# otherwise Terraform errors with:
#   "provider still indicated an unknown value for litellm_model.*.mode"

resource "litellm_model" "wildcard" {
  model_name          = "openai/*"
  base_model          = "*"
  custom_llm_provider = "openai"
  model_api_key       = "sk-fake-wildcard-key"
}

output "model_wildcard_id" {
  value = litellm_model.wildcard.id
}

