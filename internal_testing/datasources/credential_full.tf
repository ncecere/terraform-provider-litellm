# data.litellm_credential - Full heterogeneous metadata by exact name

data "litellm_credential" "full" {
  credential_name = litellm_credential.full.credential_name
}

output "ds_credential_full_id" {
  value = data.litellm_credential.full.id
}

output "ds_credential_full_info" {
  value = data.litellm_credential.full.credential_info
}

output "ds_credential_full_info_json" {
  value = data.litellm_credential.full.credential_info_json
}
