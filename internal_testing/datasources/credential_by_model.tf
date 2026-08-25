# data.litellm_credential - Exact by-model route
# model_id is an ordinary path segment; slash-containing IDs are intentionally rejected.

data "litellm_credential" "by_model" {
  credential_name = "test-credential-model-lookup"
  model_id        = litellm_model.credential_source.id
}

output "ds_credential_by_model_id" {
  value = data.litellm_credential.by_model.id
}

output "ds_credential_by_model_info_json" {
  value = data.litellm_credential.by_model.credential_info_json
}
