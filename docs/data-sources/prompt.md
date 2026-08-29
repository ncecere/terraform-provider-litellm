# litellm_prompt Data Source

Retrieves information about a specific LiteLLM prompt configuration.

## Example Usage

```hcl
data "litellm_prompt" "existing" {
  prompt_id  = "my-prompt"
  environment = "production"
  # version = 2 # Optional; omission selects the latest version.
}

output "prompt_info" {
  value = {
    integration = data.litellm_prompt.existing.prompt_integration
    type        = data.litellm_prompt.existing.prompt_type
    content     = data.litellm_prompt.existing.dotprompt_content
  }
}
```

## Argument Reference

* `prompt_id` - (Required) The base prompt ID to look up.
* `environment` - (Optional) Prompt environment. Defaults to `development`.
* `version` - (Optional) Positive version to retrieve. Omit it to select the latest version deterministically within the environment.

## Attribute Reference

* `id` - The unique identifier of the prompt.
* `prompt_id` - The prompt ID.
* `prompt_integration` - The prompt integration provider (e.g., "langfuse").
* `api_base` - Base URL for the prompt provider API.
* `provider_specific_query_params` - JSON string of provider-specific query parameters.
* `ignore_prompt_manager_model` - If true, ignore the model specified in the prompt manager.
* `ignore_prompt_manager_optional_params` - If true, ignore optional params from the prompt manager.
* `dotprompt_content` - Content for dotprompt integration.
* `prompt_type` - Type of prompt: "config" or "db".
* `environment` - Environment returned for the selected prompt.
* `version` - Selected positive version.
* `created_at` - Creation timestamp of the selected version.
* `updated_at` - Last-update timestamp of the selected version.

The data source always sends an explicit environment query. Version-specific lookup uses LiteLLM's versioned prompt ID syntax while preserving `prompt_id` as the base ID in Terraform state.
