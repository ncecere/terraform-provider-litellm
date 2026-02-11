# litellm_guardrails Data Source

Retrieves a list of all LiteLLM guardrail configurations.

## Example Usage

```hcl
data "litellm_guardrails" "all" {}

output "guardrail_count" {
  value = length(data.litellm_guardrails.all.guardrails)
}

output "guardrail_names" {
  value = [for g in data.litellm_guardrails.all.guardrails : g.guardrail_name]
}

# Find pre-call guardrails
locals {
  pre_call_guardrails = [
    for g in data.litellm_guardrails.all.guardrails : g
    if g.mode == "pre_call"
  ]
}

output "input_guardrails" {
  value = [for g in local.pre_call_guardrails : g.guardrail_name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `guardrails` - List of guardrail objects, each containing:
  * `guardrail_id` - The unique identifier.
  * `guardrail_name` - Human-readable name for the guardrail.
  * `guardrail` - The guardrail integration type.
  * `mode` - When to apply the guardrail.
  * `default_on` - Whether the guardrail is enabled by default.
  * `litellm_params` - JSON string of provider-specific configuration.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
