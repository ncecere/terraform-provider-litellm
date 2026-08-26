# Add this fixture to a configured provider module. Creating or rotating the
# mapping requires Terraform or compatible OpenTofu 1.11 or later.

variable "oidc_subject" {
  type      = string
  sensitive = true
}

resource "litellm_key" "application" {}

resource "litellm_jwt_key_mapping" "application" {
  jwt_claim_name  = "sub"
  jwt_claim_value = var.oidc_subject
  key_wo          = litellm_key.application.key
  key_wo_version  = "1"
  description     = "Application OIDC subject"
  is_active       = true
}

data "litellm_jwt_key_mapping" "application" {
  id = litellm_jwt_key_mapping.application.id
}

data "litellm_jwt_key_mappings" "all" {
  depends_on = [litellm_jwt_key_mapping.application]
}
