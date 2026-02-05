# litellm_access_group Resource

Manages a LiteLLM access group. Access groups define collections of models that can be assigned to teams or users, providing fine-grained access control to LLM resources.

## Example Usage

### Minimal Example

```hcl
resource "litellm_access_group" "basic" {
  access_group_id   = "basic-models"
  access_group_name = "Basic Models"
  members           = ["gpt-3.5-turbo"]
}
```

### Full Example

```hcl
resource "litellm_access_group" "premium" {
  access_group_id   = "premium-models"
  access_group_name = "Premium Model Access"
  description       = "Access to premium AI models for enterprise users"
  
  members = [
    "gpt-4",
    "gpt-4-turbo",
    "claude-3-opus",
    "claude-3-sonnet"
  ]
  
  metadata = jsonencode({
    tier       = "enterprise"
    cost_level = "high"
  })
}
```

### Tiered Access Groups

```hcl
# Free tier models
resource "litellm_access_group" "free_tier" {
  access_group_id   = "free-tier"
  access_group_name = "Free Tier Models"
  description       = "Models available to free users"
  
  members = [
    "gpt-3.5-turbo",
    "claude-instant"
  ]
}

# Standard tier models
resource "litellm_access_group" "standard_tier" {
  access_group_id   = "standard-tier"
  access_group_name = "Standard Tier Models"
  description       = "Models available to standard users"
  
  members = [
    "gpt-3.5-turbo",
    "gpt-4",
    "claude-instant",
    "claude-3-sonnet"
  ]
}

# Enterprise tier models
resource "litellm_access_group" "enterprise_tier" {
  access_group_id   = "enterprise-tier"
  access_group_name = "Enterprise Tier Models"
  description       = "All models available to enterprise users"
  
  members = [
    "gpt-3.5-turbo",
    "gpt-4",
    "gpt-4-turbo",
    "claude-instant",
    "claude-3-sonnet",
    "claude-3-opus"
  ]
}
```

### Access Group with Team Assignment

```hcl
resource "litellm_access_group" "dev_models" {
  access_group_id   = "development-models"
  access_group_name = "Development Models"
  members           = ["gpt-3.5-turbo", "gpt-4"]
}

resource "litellm_team" "developers" {
  team_alias    = "developers"
  models        = litellm_access_group.dev_models.members
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `access_group_id` - (Required) Unique identifier for the access group.
* `access_group_name` - (Required) Human-readable name for the access group.
* `members` - (Required) List of model names included in this access group.

### Optional Arguments

* `description` - (Optional) Description of the access group's purpose.
* `metadata` - (Optional) JSON string containing additional metadata.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this access group (same as access_group_id).
* `created_at` - Timestamp when the access group was created.
* `updated_at` - Timestamp when the access group was last updated.

## Import

Access groups can be imported using the access group ID:

```shell
terraform import litellm_access_group.example premium-models
```

## Notes

- Access groups simplify model access management across teams
- A model can belong to multiple access groups
- Use access groups to implement tiered pricing or feature access
- Changes to access group members immediately affect all associated teams
