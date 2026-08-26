data "litellm_jwt_key_mapping" "acceptance" {
  id = litellm_jwt_key_mapping.acceptance.id
}

output "jwt_key_mapping_active" {
  value = data.litellm_jwt_key_mapping.acceptance.is_active
}
