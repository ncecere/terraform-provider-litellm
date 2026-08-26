# Disposable pinned-v1.98 JWT mapping lifecycle fixture.
# The key resource is assembled in the same isolated smoke directory.
resource "litellm_jwt_key_mapping" "acceptance" {
  jwt_claim_name  = "sub"
  jwt_claim_value = "terraform-provider-local-v1.98-jwt-mapping"
  key_wo          = litellm_key.minimal.key
  key_wo_version  = "1"
  description     = "local disposable acceptance mapping"
  is_active       = true
}

output "jwt_key_mapping_id" {
  value = litellm_jwt_key_mapping.acceptance.id
}
