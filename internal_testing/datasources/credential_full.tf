# data.litellm_credential - Full
# With optional model_id filter

data "litellm_credential" "full" {
  credential_name = litellm_credential.full.credential_name
  model_id        = "gpt-4o"
}

output "ds_credential_full_id" {
  value = data.litellm_credential.full.id
}

output "ds_credential_full_info" {
  value = data.litellm_credential.full.credential_info
}
