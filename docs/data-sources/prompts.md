# litellm_prompts Data Source

Retrieves a list of all LiteLLM prompt configurations.

## Example Usage

```hcl
data "litellm_prompts" "all" {}

data "litellm_prompts" "production" {
  environment = "production"
}

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

* `environment` - (Optional) Restricts the inventory to one environment. Omission preserves LiteLLM's unscoped latest-prompt inventory.

## Attribute Reference

* `id` - Placeholder identifier.
* `prompts` - List of prompt objects, each containing:
  * `prompt_id` - The base prompt ID.
  * `environment` - Prompt environment.
  * `version` - Latest version represented by the item, when LiteLLM supplies version history.
  * `created_at` - Creation timestamp of the represented version.
  * `updated_at` - Last-update timestamp of the represented version.
  * `prompt_integration` - The prompt integration provider.
  * `api_base` - Base URL for the prompt provider API.
  * `provider_specific_query_params` - JSON string of provider-specific query parameters.
  * `ignore_prompt_manager_model` - If true, ignore the model in prompt manager.
  * `ignore_prompt_manager_optional_params` - If true, ignore optional params.
  * `prompt_type` - Type of prompt: "config" or "db".

## Environment Filtering

LiteLLM v1.98's process-local prompt registry can collapse equal base IDs and version numbers across environments. When `environment` is configured, the provider discovers visible base IDs and resolves each through the authoritative environment-scoped single-info endpoint, bounded to 200 candidates. This restores deterministic latest-version inventory for same-name cross-environment prompts. Without a filter, the data source preserves LiteLLM's unscoped registry view and may reflect that upstream collision behavior.

The v1.98 list route is unpaginated. Reads require an admin-view role; unauthorized LiteLLM callers may receive an empty list.
