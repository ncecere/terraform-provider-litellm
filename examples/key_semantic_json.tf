# Lossless heterogeneous key dictionaries. A caller-selected key or key_wo is
# required so an accepted create can be recovered through the exact identity.
resource "litellm_key" "structured_dictionaries" {
  key       = var.structured_litellm_key
  key_alias = "structured-dictionaries"

  metadata = {
    legacy_owner = "terraform"
  }

  metadata_json = jsonencode({
    deployment = {
      production = true
      revision   = 9007199254740993
      owner      = null
      regions    = ["us-east", "us-west"]
    }
  })

  config_json = jsonencode({
    feature_flags = ["streaming", "cache"]
    retries       = 3
  })

  permissions_json = jsonencode({
    routes = {
      allow = ["/chat/completions"]
      audit = true
    }
  })
}
