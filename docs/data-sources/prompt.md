# litellm_prompt Data Source

Retrieves information about a specific LiteLLM prompt configuration.

## Example Usage

```hcl
data "litellm_prompt" "existing" {
  prompt_id = "my-prompt"
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

* `prompt_id` - (Required) The prompt ID to look up.

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
