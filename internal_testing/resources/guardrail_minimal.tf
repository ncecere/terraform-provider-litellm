# litellm_guardrail - Minimal
# Only required attributes

resource "litellm_guardrail" "minimal" {
  guardrail_name = "test-guardrail-minimal"
  guardrail      = "aporia"
  mode           = "pre_call"
}

data "litellm_guardrail" "minimal" {
  guardrail_id = litellm_guardrail.minimal.id
}

data "litellm_guardrails" "registry" {
  depends_on = [litellm_guardrail.minimal]
}

output "guardrail_registry_ids" {
  value = [for guardrail in data.litellm_guardrails.registry.guardrails : guardrail.guardrail_id]
}

output "guardrail_minimal_id" {
  value = litellm_guardrail.minimal.id
}
