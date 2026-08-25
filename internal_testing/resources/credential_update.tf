# litellm_credential - Update fixture
# Acceptance automation changes "before" to "after" and verifies the owned
# nested.remove key is removed. Unmanaged-sibling hydration is covered by the
# protocol tests, not claimed by this backend fixture.

variable "credential_update_phase" {
  type    = string
  default = "before"
}

resource "litellm_credential" "update" {
  credential_name = "test-cred-update"

  credential_info = {
    phase = var.credential_update_phase
  }
  credential_info_json = jsonencode({
    nested = merge(
      { keep = var.credential_update_phase },
      var.credential_update_phase == "before" ? { remove = "owned" } : {}
    )
  })

  credential_values = {
    api_key = "sk-fake-credential-update-key"
  }
}

output "credential_update_id" {
  value = litellm_credential.update.id
}
