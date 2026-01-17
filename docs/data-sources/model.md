# litellm_model Data Source

Retrieves information about a specific LiteLLM model configuration.

## Example Usage

### Minimal Example

```hcl
data "litellm_model" "gpt4" {
  id = "model-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_model" "production_model" {
  id = "model-xxxxxxxxxxxx"
}

output "model_info" {
  value = {
    name     = data.litellm_model.production_model.model_name
    provider = data.litellm_model.production_model.custom_llm_provider
    tier     = data.litellm_model.production_model.tier
  }
}

# Use model data in another resource
resource "litellm_team" "users" {
  team_alias = "model-users"
  models     = [data.litellm_model.production_model.model_name]
}
```

## Argument Reference

The following arguments are supported:

* `id` - (Required) The unique identifier of the model to retrieve.

## Attribute Reference

The following attributes are exported:

* `model_name` - The name of the model configuration.
* `custom_llm_provider` - The LLM provider (e.g., openai, anthropic, bedrock).
* `base_model` - The actual model identifier from the provider.
* `tier` - The usage tier for this model (free, paid).
* `mode` - The model mode (chat, completion, embedding, etc.).
* `tpm` - Tokens per minute limit.
* `rpm` - Requests per minute limit.
* `model_api_base` - Base URL for the model API.
* `api_version` - API version for the model provider.
