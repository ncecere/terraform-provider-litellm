# litellm_models Data Source

Retrieves a list of all LiteLLM model configurations.

## Example Usage

### Minimal Example

```hcl
data "litellm_models" "all" {}
```

### Full Example

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

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `models` - List of model objects, each containing:
  * `id` - The unique identifier of the model.
  * `model_name` - The name of the model configuration.
  * `custom_llm_provider` - The LLM provider.
  * `base_model` - The actual model identifier.
  * `tier` - The usage tier (free, paid).
  * `mode` - The model mode.
