# data.litellm_guardrails - Lists all guardrails

data "litellm_guardrails" "all" {
  depends_on = [litellm_guardrail.minimal]
}

output "ds_guardrails_list" {
  value     = data.litellm_guardrails.all
  sensitive = true
}
