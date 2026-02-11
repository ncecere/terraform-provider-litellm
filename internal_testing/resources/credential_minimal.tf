# litellm_credential - Minimal
# Only required attributes

resource "litellm_credential" "minimal" {
  credential_name = "test-cred-minimal"

  credential_info = {
    "description" = "Minimal test credential"
  }

  credential_values = {
    "api_key" = "sk-fake-credential-key"
  }
}

output "credential_minimal_id" {
  value = litellm_credential.minimal.id
}
