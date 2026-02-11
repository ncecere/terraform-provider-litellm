# data.litellm_guardrail - Looks up a guardrail by guardrail_id
# Note: guardrail_id must reference an existing guardrail

data "litellm_guardrail" "lookup" {
  guardrail_id = litellm_guardrail.minimal.id
}

output "ds_guardrail_name" {
  value = data.litellm_guardrail.lookup.guardrail_name
}

output "ds_guardrail_guardrail" {
  value = data.litellm_guardrail.lookup.guardrail
}

output "ds_guardrail_mode" {
  value = data.litellm_guardrail.lookup.mode
}

output "ds_guardrail_default_on" {
  value = data.litellm_guardrail.lookup.default_on
}

output "ds_guardrail_created_at" {
  value = data.litellm_guardrail.lookup.created_at
}
