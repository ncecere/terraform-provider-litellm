# litellm_policy Resource

Manages a LiteLLM policy.

## Example Usage

### Minimal Policy

```hcl
resource "litellm_policy" "minimal" {
  policy_name = "global-baseline"
}
```

### Policy with Guardrails, Condition, and Pipeline

```hcl
resource "litellm_policy" "healthcare" {
  policy_name = "healthcare-compliance"
  inherit     = "global-baseline"
  description = "Policy for healthcare workloads"

  guardrails_add    = ["hipaa_audit", "pii_masking"]
  guardrails_remove = ["prompt_injection"]

  condition = {
    model = "gpt-4.*"
  }

  pipeline = jsonencode({
    mode = "pre_call"
    steps = [
      {
        guardrail = "pii_masking"
        on_fail   = "block"
      }
    ]
  })
}
```

## Argument Reference

* `policy_name` - (Required) Unique policy name.
* `inherit` - (Optional) Name of parent policy to inherit from.
* `description` - (Optional) Human-readable policy description.
* `guardrails_add` - (Optional) List of guardrails to add.
* `guardrails_remove` - (Optional) List of guardrails to remove from inherited set.
* `condition` - (Optional) Policy condition block.
  * `model` - (Optional) Model name pattern (exact match or regex) for when the policy applies.
* `pipeline` - (Optional) JSON string defining optional guardrail pipeline.

## Attribute Reference

* `id` - The policy ID.
* `version_number` - Version number of this policy.
* `version_status` - Version status (`draft`, `published`, `production`).
* `parent_version_id` - Policy ID this version was cloned from.
* `is_latest` - Whether this is the latest version.
* `published_at` - Timestamp when this version was published.
* `production_at` - Timestamp when this version was promoted to production.
* `created_at` - Timestamp when the policy was created.
* `updated_at` - Timestamp when the policy was last updated.
* `created_by` - Who created the policy.
* `updated_by` - Who last updated the policy.

## Import

Policies can be imported using their policy ID:

```shell
terraform import litellm_policy.example <policy-id>
```
