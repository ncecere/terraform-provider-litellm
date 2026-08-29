# litellm_credential - Full heterogeneous values create
# Exercises disjoint legacy-map and canonical JSON ownership.

resource "litellm_credential" "full" {
  credential_name = "test-cred-full"

  credential_info = {
    description = "Full test credential"
    provider    = "openai"
  }
  credential_info_json = jsonencode({
    enabled = true
    labels  = ["test", "full"]
    limits = {
      retries = 3
    }
  })

  credential_values = {
    api_key  = "sk-fake-credential-key-full"
    api_base = "https://api.openai.com/v1"
  }
  credential_values_json = jsonencode({
    oauth = {
      client_id     = "fake-client"
      client_secret = "fake-secret"
    }
  })
}

output "credential_full_id" {
  value = litellm_credential.full.id
}

output "credential_full_info_json" {
  value = litellm_credential.full.credential_info_json
}
