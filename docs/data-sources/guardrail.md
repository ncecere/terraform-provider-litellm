# litellm_guardrail Data Source

Retrieves information about a specific LiteLLM guardrail configuration.

## Example Usage

```hcl
data "litellm_guardrail" "existing" {
  guardrail_id = "my-guardrail-id"
}

output "guardrail_info" {
  value = {
    name       = data.litellm_guardrail.existing.guardrail_name
    guardrail  = data.litellm_guardrail.existing.guardrail
    mode       = data.litellm_guardrail.existing.mode
    default_on = data.litellm_guardrail.existing.default_on
  }
}
```

## Argument Reference

* `guardrail_id` - (Required) The unique identifier of the guardrail to retrieve.

## Attribute Reference

* `id` - The unique identifier of the guardrail.
* `guardrail_id` - The guardrail ID.
* `guardrail_name` - Human-readable name for the guardrail.
* `guardrail` - The guardrail integration type (e.g., "aporia", "bedrock", "lakera").
* `mode` - When to apply the guardrail (e.g., "pre_call", "post_call", "during_call", or a JSON array of modes).
* `default_on` - Whether the guardrail is enabled by default for all requests.
* `litellm_params` - JSON string containing additional provider-specific parameters.
* `guardrail_info` - JSON string containing additional guardrail metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
