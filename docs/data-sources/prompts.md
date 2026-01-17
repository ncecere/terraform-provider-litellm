# litellm_prompts Data Source

Retrieves a list of all LiteLLM prompt templates.

## Example Usage

### Minimal Example

```hcl
data "litellm_prompts" "all" {}
```

### Full Example

```hcl
data "litellm_prompts" "all" {}

output "prompt_count" {
  value = length(data.litellm_prompts.all.prompts)
}

output "prompt_names" {
  value = [for p in data.litellm_prompts.all.prompts : p.prompt_name]
}

# Find support-related prompts
locals {
  support_prompts = [
    for p in data.litellm_prompts.all.prompts : p
    if can(regex("support", lower(p.prompt_name)))
  ]
}

output "support_prompt_names" {
  value = [for p in local.support_prompts : p.prompt_name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `prompts` - List of prompt objects, each containing:
  * `prompt_id` - The unique identifier.
  * `prompt_name` - The prompt name.
  * `prompt` - The prompt text content.
  * `description` - Description.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
