# litellm_credential - Full
# All attributes populated

resource "litellm_credential" "full" {
  credential_name = "test-cred-full"
  model_id        = "gpt-4o"

  credential_info = {
    "description" = "Full test credential"
    "provider"    = "openai"
  }

  credential_values = {
    "api_key"  = "sk-fake-credential-key-full"
    "api_base" = "https://api.openai.com/v1"
  }
}

output "credential_full_id" {
  value = litellm_credential.full.id
}
