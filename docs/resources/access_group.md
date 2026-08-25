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

~> **Important:** `model_names` must contain at least one non-null value, and every name must identify a model deployment that exists in LiteLLM. This includes an empty name: `""` is valid only when an empty-named deployment exists. Use resource references (as shown above) to ensure dependency ordering and that models are created before the access group.

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
- `model_names` - (Required) A non-empty list of non-null model names to include in the access group. Duplicate values and `""` are accepted for compatibility with LiteLLM v1.98; an empty name must correspond to an existing empty-named deployment. LiteLLM treats this as unordered, deduplicated membership: the provider sends sorted, deduplicated names and preserves the configured list—including its order and duplicates—when its unique membership matches read-back. Actual membership changes remain visible as a deterministic sorted, deduplicated list.

## List Compatibility

`model_names` remains a Terraform list. Existing indexing, `concat(...)`, outputs, and `list(string)` module inputs continue to work without conversion.

## Deployment IDs

LiteLLM v1.98 accepts deployment-level `model_ids` on create and update, but its access-group info and list responses return only deduplicated `model_names`. The provider therefore cannot read deployment identity back without potentially broadening membership to every deployment sharing a name. `model_ids` remains deferred until LiteLLM exposes stable deployment-ID membership through its read endpoints.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

- `id` - The internal resource identifier.

## Import

Access groups can be imported using the access group name:

```shell
terraform import litellm_access_group.example <access-group-name>
```
