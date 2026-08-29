data "litellm_jwt_key_mappings" "acceptance" {
  depends_on = [litellm_jwt_key_mapping.acceptance]
}

output "jwt_key_mapping_count" {
  value = length(data.litellm_jwt_key_mappings.acceptance.mappings)
}
