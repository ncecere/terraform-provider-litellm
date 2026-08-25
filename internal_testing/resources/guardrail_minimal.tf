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

  lifecycle {
    postcondition {
      condition     = contains([for guardrail in self.guardrails : guardrail.guardrail_id], litellm_guardrail.minimal.id)
      error_message = "The v2 guardrail registry omitted the Terraform-managed guardrail."
    }
  }
}

output "guardrail_minimal_id" {
  value = litellm_guardrail.minimal.id
}
