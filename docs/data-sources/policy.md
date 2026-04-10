# litellm_policy Data Source

Retrieves information about a LiteLLM policy by policy ID.

## Example Usage

```hcl
data "litellm_policy" "existing" {
  policy_id = "123e4567-e89b-12d3-a456-426614174000"
}

output "policy_name" {
  value = data.litellm_policy.existing.policy_name
}

output "policy_condition_model" {
  value = data.litellm_policy.existing.condition.model
}
```

## Argument Reference

* `policy_id` - (Required) The policy ID to retrieve.

## Attribute Reference

* `id` - The policy ID.
* `policy_name` - Policy name.
* `inherit` - Name of parent policy.
* `description` - Policy description.
* `guardrails_add` - Guardrails to add.
* `guardrails_remove` - Guardrails to remove from inherited set.
* `condition` - Policy condition block.
  * `model` - Model name pattern (exact match or regex).
* `pipeline` - JSON string defining optional guardrail pipeline.
* `version_number` - Version number.
* `version_status` - Version status (`draft`, `published`, `production`).
* `parent_version_id` - Policy ID this version was cloned from.
* `is_latest` - Whether this is the latest version.
* `published_at` - Timestamp when this version was published.
* `production_at` - Timestamp when this version was promoted to production.
* `created_at` - Timestamp when policy was created.
* `updated_at` - Timestamp when policy was last updated.
* `created_by` - Who created the policy.
* `updated_by` - Who last updated the policy.
