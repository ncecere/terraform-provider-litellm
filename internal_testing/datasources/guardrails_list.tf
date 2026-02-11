# data.litellm_guardrails - Lists all guardrails

data "litellm_guardrails" "all" {
}

output "ds_guardrails_list" {
  value = data.litellm_guardrails.all
}
