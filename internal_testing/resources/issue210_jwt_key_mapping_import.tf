# Source-free JWT mapping import deliberately omits write-only create credentials.
resource "terraform_data" "jwt_mapping_run" {
  input = uuid()

  lifecycle {
    ignore_changes = [input]
  }
}

resource "litellm_jwt_key_mapping" "acceptance" {
  jwt_claim_name  = "sub-${terraform_data.jwt_mapping_run.output}"
  jwt_claim_value = "terraform-provider-local-v1.98-${terraform_data.jwt_mapping_run.output}"
  description     = "local disposable acceptance mapping"
  is_active       = false
}
