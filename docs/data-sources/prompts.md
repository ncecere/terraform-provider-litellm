# litellm_prompts Data Source

Retrieves a list of all LiteLLM prompt configurations.

## Example Usage

```hcl
data "litellm_prompts" "all" {}

output "prompt_count" {
  value = length(data.litellm_prompts.all.prompts)
}

output "prompt_ids" {
  value = [for p in data.litellm_prompts.all.prompts : p.prompt_id]
}

# Find prompts using langfuse integration
locals {
  langfuse_prompts = [
    for p in data.litellm_prompts.all.prompts : p
    if p.prompt_integration == "langfuse"
  ]
}

output "langfuse_prompt_ids" {
  value = [for p in local.langfuse_prompts : p.prompt_id]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `prompts` - List of prompt objects, each containing:
  * `prompt_id` - The prompt ID.
  * `prompt_integration` - The prompt integration provider.
  * `api_base` - Base URL for the prompt provider API.
  * `provider_specific_query_params` - JSON string of provider-specific query parameters.
  * `ignore_prompt_manager_model` - If true, ignore the model in prompt manager.
  * `ignore_prompt_manager_optional_params` - If true, ignore optional params.
  * `prompt_type` - Type of prompt: "config" or "db".
