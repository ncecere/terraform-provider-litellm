# litellm_key - lossless heterogeneous key dictionaries
# A caller-selected key keeps accepted-create recovery identity-bound.
resource "litellm_key" "semantic_json" {
  key       = "sk-semantic-json-lifecycle-20260828"
  key_alias = "semantic-json-lifecycle"
  models    = ["gpt-4o-mini"]

  metadata = {
    legacy_owner = "terraform"
  }

  metadata_json = jsonencode({
    deployment = {
      production = true
      revision   = 9007199254740993
      owner      = null
      regions    = ["us-east", "us-west"]
      empty      = {}
    }
  })

  config_json = jsonencode({
    feature_flags = ["streaming", "cache"]
    retries       = 3
    threshold     = 0.5
  })

  permissions_json = jsonencode({
    routes = {
      allow = ["/chat/completions"]
      audit = true
    }
  })
}

output "key_semantic_json_id" {
  value = litellm_key.semantic_json.id
}
