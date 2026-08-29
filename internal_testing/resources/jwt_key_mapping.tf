# Disposable pinned-v1.98 JWT mapping lifecycle fixture.
# The key resource is assembled in the same isolated smoke directory. The
# built-in terraform_data resource fixes one random pair per isolated run.
resource "terraform_data" "jwt_mapping_run" {
  input = uuid()

  lifecycle {
    ignore_changes = [input]
  }
}

resource "litellm_jwt_key_mapping" "acceptance" {
  jwt_claim_name  = "sub-${terraform_data.jwt_mapping_run.output}"
  jwt_claim_value = "terraform-provider-local-v1.98-${terraform_data.jwt_mapping_run.output}"
  key_wo          = litellm_key.minimal.key
  key_wo_version  = "1"
  description     = "local disposable acceptance mapping"
  is_active       = false
}

output "jwt_key_mapping_id" {
  value = litellm_jwt_key_mapping.acceptance.id
}
