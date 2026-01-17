# litellm_tag Data Source

Retrieves information about a specific LiteLLM tag.

## Example Usage

### Minimal Example

```hcl
data "litellm_tag" "existing" {
  name = "production"
}
```

### Full Example

```hcl
data "litellm_tag" "environment" {
  name = "production"
}

output "tag_info" {
  value = {
    name        = data.litellm_tag.environment.name
    description = data.litellm_tag.environment.description
  }
}
```

## Argument Reference

The following arguments are supported:

* `name` - (Required) The name of the tag to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the tag.
* `name` - The tag name.
* `description` - Description of the tag.
* `metadata` - JSON string containing additional metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
