# litellm_prompt (Resource)

Manages a LiteLLM prompt. Prompts allow you to store reusable prompt templates that can be used across different models and applications via the LiteLLM prompt manager.

## Example Usage

### Minimal Example

```hcl
resource "litellm_prompt" "minimal" {
  prompt_id          = "my-prompt"
  prompt_integration = "dotprompt"
}
```

### Full Example with Dotprompt Content

```hcl
resource "litellm_prompt" "full" {
  prompt_id          = "customer-support"
  prompt_integration = "dotprompt"
  prompt_type        = "db"

  dotprompt_content = <<-EOT
    ---
    model: gpt-4o
    ---
    You are a helpful assistant. Answer the user's question concisely.
    {{question}}
  EOT

  ignore_prompt_manager_model           = false
  ignore_prompt_manager_optional_params = false
}
```

### Prompt with API Configuration

```hcl
resource "litellm_prompt" "with_api" {
  prompt_id          = "external-prompt"
  prompt_integration = "dotprompt"
  prompt_type        = "db"

  api_base = "https://my-litellm-instance.example.com"
  api_key  = var.litellm_api_key

  dotprompt_content = <<-EOT
    ---
    model: gpt-4o-mini
    ---
    Summarize the following text in {{language}}:
    {{text}}
  EOT

  ignore_prompt_manager_model = true
}
```

## Argument Reference

The following arguments are supported:

### Required

* `prompt_id` - (Required, ForceNew) Unique identifier for the prompt. Changing this forces creation of a new resource.
* `prompt_integration` - (Required) The prompt integration type (e.g., `"dotprompt"`).

### Optional

* `dotprompt_content` - (Optional) The dotprompt-formatted content for the prompt. Supports YAML frontmatter for model configuration and Mustache-style `{{variable}}` template syntax.
* `prompt_type` - (Optional) The type of prompt storage (e.g., `"db"` for database-stored prompts).
* `api_base` - (Optional) API base URL for the prompt manager endpoint.
* `api_key` - (Optional, Sensitive) API key for authenticating with the prompt manager endpoint.
* `provider_specific_query_params` - (Optional) Provider-specific query parameters to pass through.
* `ignore_prompt_manager_model` - (Optional) When `true`, ignores the model specified in the prompt manager and uses the caller's model instead.
* `ignore_prompt_manager_optional_params` - (Optional) When `true`, ignores optional parameters specified in the prompt manager.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The identifier of the prompt.

## Import

Prompts can be imported using the prompt ID:

```shell
terraform import litellm_prompt.example <prompt-id>
```

## Notes

* Prompt IDs must be unique within the LiteLLM instance.
* Use heredoc syntax (`<<-EOT`) for multi-line dotprompt content.
* Dotprompt content supports YAML frontmatter (between `---` delimiters) for specifying model and parameter defaults.
* Template variables use Mustache-style `{{variable}}` syntax within the prompt body.
