# litellm_access_group (Resource)

Manages an access group in LiteLLM. Access groups define collections of models that can be referenced together when assigning model access to keys or teams.

## Example Usage

```hcl
resource "litellm_model" "gpt4" {
  model_name          = "gpt-4o-mini"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_access_group" "example" {
  access_group = "basic-models"
  model_names  = [litellm_model.gpt4.model_name]
}
```

~> **Important:** `model_names` must reference models that actually exist in LiteLLM. If you reference non-existent models, the access group will be created but won't function correctly. Use resource references (as shown above) to ensure proper dependency ordering and that models are created before the access group.

### Multiple Models

```hcl
resource "litellm_model" "gpt4" {
  model_name          = "gpt-4o-mini"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_model" "claude" {
  model_name          = "claude-sonnet"
  custom_llm_provider = "anthropic"
  base_model          = "claude-sonnet-4-20250514"
}

resource "litellm_access_group" "all_models" {
  access_group = "all-models"
  model_names  = [
    litellm_model.gpt4.model_name,
    litellm_model.claude.model_name,
  ]
}
```

## Argument Reference

The following arguments are supported:

- `access_group` - (Required, ForceNew) The name of the access group. Changing this value forces creation of a new resource.
- `model_names` - (Required) A list of model names to include in the access group. Each model must exist in LiteLLM.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

- `id` - The internal resource identifier.

## Import

Access groups can be imported using the access group name:

```shell
terraform import litellm_access_group.example <access-group-name>
```
