# litellm_credential - True model-only create
# credential_values is optional; model_id is the sole source and wins honestly.

resource "litellm_model" "credential_source" {
  model_name          = "test-credential-source-model"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  model_api_key       = "sk-fake-model-source-key"
}

resource "litellm_credential" "model" {
  credential_name = "test-cred-model"
  model_id        = litellm_model.credential_source.id
}

output "credential_model_id" {
  value = litellm_credential.model.id
}

output "credential_model_values_active" {
  value = litellm_credential.model.credential_values_active
}
