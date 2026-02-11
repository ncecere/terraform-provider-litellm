# litellm_guardrail - Minimal
# Only required attributes

resource "litellm_guardrail" "minimal" {
  guardrail_name = "test-guardrail-minimal"
  guardrail      = "aporia"
  mode           = "pre_call"
}

output "guardrail_minimal_id" {
  value = litellm_guardrail.minimal.id
}
