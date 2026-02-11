# litellm_model - Minimal
# Only required attributes

resource "litellm_model" "minimal" {
  model_name          = "test-model-minimal"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

output "model_minimal_id" {
  value = litellm_model.minimal.id
}
