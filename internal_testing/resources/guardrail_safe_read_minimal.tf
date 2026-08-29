# litellm_guardrail - Dedicated ordinary-read/external-delete lifecycle

resource "litellm_guardrail" "safe_read" {
  guardrail_name = "acceptance-guardrail-safe-read"
  guardrail      = "aporia"
  mode           = "pre_call"
}

output "guardrail_safe_read_id" {
  value = litellm_guardrail.safe_read.id
}
