# data.litellm_credential - Minimal
# Only required lookup key

data "litellm_credential" "minimal" {
  credential_name = litellm_credential.minimal.credential_name
}

output "ds_credential_minimal_id" {
  value = data.litellm_credential.minimal.id
}

output "ds_credential_minimal_info" {
  value = data.litellm_credential.minimal.credential_info
}
