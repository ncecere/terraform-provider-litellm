variable "replacement_phase" {
  type    = string
  default = "before"

  validation {
    condition     = contains(["before", "after"], var.replacement_phase)
    error_message = "replacement_phase must be before or after."
  }
}

resource "litellm_key" "replacement_dependency" {
  key_alias = "issue210-jwt-replacement-dependency"
}

resource "litellm_jwt_key_mapping" "replacement" {
  jwt_claim_name  = var.replacement_phase == "before" ? "issue210-before" : "issue210-after"
  jwt_claim_value = var.replacement_phase == "before" ? "issue210-before-value" : "issue210-after-value"
  key_wo          = litellm_key.replacement_dependency.key
  key_wo_version  = "1"
  description     = "issue210 JWT replacement proof"
  is_active       = false

  lifecycle {
    create_before_destroy = true
  }
}
