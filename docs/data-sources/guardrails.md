# litellm_guardrails Data Source

Retrieves a list of all LiteLLM guardrail configurations.

## Example Usage

### Minimal Example

```hcl
data "litellm_guardrails" "all" {}
```

### Full Example

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
    if can(regex("pre_call", g.litellm_params))
  ]
}

output "input_guardrails" {
  value = [for g in local.pre_call_guardrails : g.guardrail_name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `guardrails` - List of guardrail objects, each containing:
  * `guardrail_id` - The unique identifier.
  * `guardrail_name` - The guardrail name.
  * `litellm_params` - JSON string of configuration.
  * `description` - Description.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
