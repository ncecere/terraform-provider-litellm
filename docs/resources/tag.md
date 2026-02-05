# litellm_tag Resource

Manages a LiteLLM tag. Tags allow you to categorize and organize resources like models, teams, and API keys for reporting and access control purposes.

## Example Usage

### Minimal Example

```hcl
resource "litellm_tag" "basic" {
  name = "production"
}
```

### Full Example

```hcl
resource "litellm_tag" "environment" {
  name        = "production"
  description = "Production environment resources"
  
  metadata = jsonencode({
    priority = "high"
    sla      = "99.9%"
  })
}
```

### Multiple Tags for Organization

```hcl
resource "litellm_tag" "environments" {
  for_each    = toset(["development", "staging", "production"])
  name        = each.value
  description = "${each.value} environment"
}

resource "litellm_tag" "departments" {
  for_each    = toset(["engineering", "marketing", "sales", "support"])
  name        = "dept-${each.value}"
  description = "${title(each.value)} department resources"
}
```

### Tag with Model Association

```hcl
resource "litellm_tag" "ai_tier" {
  name        = "premium-ai"
  description = "Premium AI model tier"
}

resource "litellm_model" "gpt4" {
  model_name          = "gpt-4-premium"
  custom_llm_provider = "openai"
  base_model          = "gpt-4"
  model_api_key       = var.openai_api_key
  
  # Associate tag via metadata
  metadata = jsonencode({
    tags = [litellm_tag.ai_tier.name]
  })
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `name` - (Required) Name of the tag. Must be unique.

### Optional Arguments

* `description` - (Optional) Description of the tag's purpose.
* `metadata` - (Optional) JSON string containing additional metadata for the tag.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this tag.
* `created_at` - Timestamp when the tag was created.
* `updated_at` - Timestamp when the tag was last updated.

## Import

Tags can be imported using the tag name:

```shell
terraform import litellm_tag.example production
```

## Notes

- Tag names must be unique within the LiteLLM instance
- Tags can be used for cost allocation and reporting
- Resources can have multiple tags associated with them
- Use tags to implement RBAC-style access control patterns
