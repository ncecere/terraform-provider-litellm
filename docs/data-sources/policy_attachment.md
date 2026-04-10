# litellm_policy_attachment Data Source

Retrieves information about a policy attachment by attachment ID.

## Example Usage

```hcl
data "litellm_policy_attachment" "existing" {
  attachment_id = "123e4567-e89b-12d3-a456-426614174000"
}

output "attachment_policy_name" {
  value = data.litellm_policy_attachment.existing.policy_name
}
```

## Argument Reference

* `attachment_id` - (Required) Attachment ID to retrieve.

## Attribute Reference

* `id` - The attachment ID.
* `policy_name` - Name of attached policy.
* `scope` - Attachment scope.
* `teams` - Team patterns.
* `keys` - Key patterns.
* `models` - Model patterns.
* `tags` - Tag patterns.
* `created_at` - When the attachment was created.
* `updated_at` - When the attachment was last updated.
* `created_by` - Who created the attachment.
* `updated_by` - Who last updated the attachment.
