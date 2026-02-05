# litellm_guardrail Data Source

Retrieves information about a specific LiteLLM guardrail configuration.

## Example Usage

### Minimal Example

```hcl
data "litellm_guardrail" "existing" {
  guardrail_name = "content-safety"
}
```

### Full Example

```hcl
data "litellm_guardrail" "safety_filter" {
  guardrail_name = "content-safety-filter"
}

output "guardrail_info" {
  value = {
    name   = data.litellm_guardrail.safety_filter.guardrail_name
    config = data.litellm_guardrail.safety_filter.litellm_params
  }
}
```

## Argument Reference

The following arguments are supported:

* `guardrail_name` - (Required) The name of the guardrail to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the guardrail.
* `guardrail_id` - The guardrail ID.
* `guardrail_name` - The guardrail name.
* `litellm_params` - JSON string containing guardrail configuration.
* `description` - Description of the guardrail.
* `metadata` - JSON string containing additional metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
