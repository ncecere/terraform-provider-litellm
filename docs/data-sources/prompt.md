# litellm_prompt Data Source

Retrieves information about a specific LiteLLM prompt template.

## Example Usage

### Minimal Example

```hcl
data "litellm_prompt" "existing" {
  prompt_name = "customer-support"
}
```

### Full Example

```hcl
data "litellm_prompt" "support_agent" {
  prompt_name = "customer-support-agent"
}

output "prompt_info" {
  value = {
    name        = data.litellm_prompt.support_agent.prompt_name
    content     = data.litellm_prompt.support_agent.prompt
    description = data.litellm_prompt.support_agent.description
  }
}

# Use prompt in application
locals {
  system_prompt = data.litellm_prompt.support_agent.prompt
}
```

## Argument Reference

The following arguments are supported:

* `prompt_name` - (Required) The name of the prompt to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the prompt.
* `prompt_id` - The prompt ID.
* `prompt_name` - The prompt name.
* `prompt` - The prompt text content.
* `description` - Description of the prompt.
* `metadata` - JSON string containing additional metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
