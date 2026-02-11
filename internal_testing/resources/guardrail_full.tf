# litellm_guardrail - Full
# All attributes populated

resource "litellm_guardrail" "full" {
  guardrail_name = "test-guardrail-full"
  guardrail      = "bedrock"
  mode           = "pre_call"
  default_on     = true

  litellm_params = jsonencode({
    "guardrailIdentifier" = "test-guardrail-id"
    "guardrailVersion"    = "1"
  })

  guardrail_info = jsonencode({
    "description" = "Full test guardrail"
  })
}

output "guardrail_full_id" {
  value = litellm_guardrail.full.id
}

output "guardrail_full_created_at" {
  value = litellm_guardrail.full.created_at
}
