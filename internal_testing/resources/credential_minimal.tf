# litellm_credential - Minimal non-empty values-only create
# LiteLLM v1.98 rejects an empty values-only object by truthiness.

resource "litellm_credential" "minimal" {
  credential_name = "test-cred-minimal"
  credential_values = {
    api_key = "sk-fake-credential-key-minimal"
  }
}

output "credential_minimal_id" {
  value = litellm_credential.minimal.id
}

output "credential_minimal_source" {
  value = litellm_credential.minimal.credential_source
}
