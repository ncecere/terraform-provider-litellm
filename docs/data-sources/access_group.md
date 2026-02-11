# litellm_access_group Data Source

Retrieves information about a specific LiteLLM access group.

## Example Usage

```hcl
data "litellm_access_group" "existing" {
  access_group = "premium-models"
}

output "models_in_group" {
  value = data.litellm_access_group.existing.model_names
}

# Create team with same model access
resource "litellm_team" "premium_team" {
  team_alias = "premium-users"
  models     = data.litellm_access_group.existing.model_names
}
```

## Argument Reference

* `access_group` - (Required) The name of the access group to look up.

## Attribute Reference

* `id` - The unique identifier of the access group.
* `model_names` - List of model names included in this access group.
