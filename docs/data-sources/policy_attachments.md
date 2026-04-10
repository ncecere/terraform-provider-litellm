# litellm_policy_attachments Data Source

Retrieves a list of policy attachments.

## Example Usage

```hcl
data "litellm_policy_attachments" "all" {}

output "attachment_ids" {
  value = [for a in data.litellm_policy_attachments.all.attachments : a.attachment_id]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `total_count` - Total number of returned attachments.
* `attachments` - List of attachment objects, each containing:
  * `attachment_id` - Attachment ID.
  * `policy_name` - Name of attached policy.
  * `scope` - Attachment scope.
  * `teams` - Team patterns.
  * `keys` - Key patterns.
  * `models` - Model patterns.
  * `tags` - Tag patterns.
  * `created_at` - When created.
  * `updated_at` - When updated.
  * `created_by` - Creator.
  * `updated_by` - Last updater.
