# litellm_model Data Source

Retrieves information about a specific LiteLLM model configuration.

## Example Usage

```hcl
data "litellm_model" "existing" {
  model_id = "model-xxxxxxxxxxxx"
}

output "model_info" {
  value = {
    name     = data.litellm_model.existing.model_name
    provider = data.litellm_model.existing.custom_llm_provider
    tier     = data.litellm_model.existing.tier
  }
}

# Use model data in another resource
resource "litellm_team" "users" {
  team_alias = "model-users"
  models     = [data.litellm_model.existing.model_name]
}
```

## Argument Reference

* `model_id` - (Required) The model ID to look up (litellm_model_id).

## Attribute Reference

* `id` - The unique identifier of the model.
* `model_name` - The name of the model configuration.
* `custom_llm_provider` - The LLM provider (e.g., openai, anthropic, bedrock).
* `base_model` - The actual model identifier from the provider.
* `tier` - The usage tier for this model (free, paid).
* `mode` - The model mode (chat, completion, embedding, etc.).
* `team_id` - Team ID associated with this model.
* `tpm` - Tokens per minute limit.
* `rpm` - Requests per minute limit.
* `model_api_base` - Base URL for the model API.
* `api_version` - API version for the model provider.
* `aws_region_name` - AWS region name for Bedrock models.
