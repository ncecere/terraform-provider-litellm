# litellm_model - Lossless heterogeneous model_info JSON

resource "litellm_model" "semantic_json" {
  model_name          = "test-model-semantic-json"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"

  additional_model_info = {
    legacy_disjoint = "legacy-value"
  }

  additional_model_info_json = <<JSON
{
  "string_false": "false",
  "native_false": false,
  "leading_zero": "001",
  "large_number": 9007199254740993,
  "decimal": 0.1,
  "nested": {
    "nullable": null,
    "items": [1, true, "1"]
  },
  "literal_mask": "****"
}
JSON
}

output "model_semantic_json_id" {
  value = litellm_model.semantic_json.id
}

output "model_semantic_json_info" {
  value     = litellm_model.semantic_json.additional_model_info_json
  sensitive = true
}
