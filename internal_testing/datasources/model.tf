# data.litellm_model - Looks up a model by model_id
# Note: model_id must reference an existing model

data "litellm_model" "lookup" {
  model_id = litellm_model.minimal.id
}

output "ds_model_name" {
  value = data.litellm_model.lookup.model_name
}

output "ds_model_provider" {
  value = data.litellm_model.lookup.custom_llm_provider
}

output "ds_model_base_model" {
  value = data.litellm_model.lookup.base_model
}

output "ds_model_tier" {
  value = data.litellm_model.lookup.tier
}

output "ds_model_mode" {
  value = data.litellm_model.lookup.mode
}
