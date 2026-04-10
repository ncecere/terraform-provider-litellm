# litellm_policies Data Source

Retrieves a list of LiteLLM policies.

## Example Usage

```hcl
data "litellm_policies" "all" {}

output "policy_names" {
  value = [for p in data.litellm_policies.all.policies : p.policy_name]
}

data "litellm_policies" "production" {
  version_status = "production"
}

output "production_policy_count" {
  value = data.litellm_policies.production.total_count
}
```

## Argument Reference

* `version_status` - (Optional) Filter by version status (`draft`, `published`, `production`).

## Attribute Reference

* `id` - Placeholder identifier.
* `total_count` - Total number of returned policies.
* `policies` - List of policy objects, each containing:
  * `policy_id` - Policy ID.
  * `policy_name` - Policy name.
  * `version_number` - Version number.
  * `version_status` - Version status.
  * `parent_version_id` - Parent version ID.
  * `is_latest` - Whether this is latest.
  * `inherit` - Parent policy name.
  * `description` - Policy description.
  * `guardrails_add` - Guardrails to add.
  * `guardrails_remove` - Guardrails to remove.
  * `condition` - Condition as JSON string.
  * `pipeline` - Pipeline as JSON string.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
  * `created_by` - Creator.
  * `updated_by` - Last updater.
