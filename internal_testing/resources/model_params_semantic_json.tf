# litellm_model - Lossless heterogeneous litellm_params JSON

resource "litellm_model" "params_semantic_json" {
  model_name          = "test-model-params-semantic-json"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"

  additional_litellm_params = {
    legacy_disjoint = "legacy-value"
  }

  additional_litellm_params_json = <<JSON
{
  "string_false": "false",
  "native_false": false,
  "leading_zero": "001",
  "large_number": 9007199254740993,
  "decimal": 0.1,
  "options": {
    "null_text": "null",
    "items": [1, true, "1"],
    "api_secret": "authoritative-plaintext",
    "safe_literal_mask": "****"
  }
}
JSON
}

output "model_params_semantic_json_id" {
  value = litellm_model.params_semantic_json.id
}

output "model_params_semantic_json_params" {
  value     = litellm_model.params_semantic_json.additional_litellm_params_json
  sensitive = true
}
