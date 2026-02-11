# data.litellm_models - Lists all models

data "litellm_models" "all" {
}

output "ds_models_list" {
  value = data.litellm_models.all
}
