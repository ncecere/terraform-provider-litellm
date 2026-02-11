# litellm_models Data Source

Retrieves a list of all LiteLLM model configurations.

## Example Usage

```hcl
data "litellm_models" "all" {}

output "model_count" {
  value = length(data.litellm_models.all.models)
}

output "model_names" {
  value = [for m in data.litellm_models.all.models : m.model_name]
}

# Filter models by provider
locals {
  openai_models = [
    for m in data.litellm_models.all.models : m
    if m.custom_llm_provider == "openai"
  ]
}

output "openai_model_names" {
  value = [for m in local.openai_models : m.model_name]
}
```

### Filter by Team

```hcl
data "litellm_models" "team_models" {
  team_id = "team-xxxxxxxxxxxx"
}
```

## Argument Reference

* `team_id` - (Optional) Filter models by team ID.

## Attribute Reference

* `id` - Placeholder identifier.
* `models` - List of model objects, each containing:
  * `id` - The unique identifier of the model.
  * `model_name` - The name of the model configuration.
  * `custom_llm_provider` - The LLM provider.
  * `base_model` - The actual model identifier.
  * `tier` - The usage tier (free, paid).
  * `mode` - The model mode.
  * `team_id` - Team ID associated with this model.
