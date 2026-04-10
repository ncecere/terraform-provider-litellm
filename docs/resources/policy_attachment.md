# litellm_policy_attachment Resource

Manages a LiteLLM policy attachment.

## Example Usage

### Global Scope Attachment

```hcl
resource "litellm_policy_attachment" "global" {
  policy_name = litellm_policy.minimal.policy_name
  scope       = "*"
}
```

### Targeted Attachment

```hcl
resource "litellm_policy_attachment" "targeted" {
  policy_name = litellm_policy.minimal.policy_name
  teams       = ["team-a", "team-b"]
  models      = ["gpt-4o", "gpt-4o-mini"]
  tags        = ["health-*", "prod"]
}
```

## Argument Reference

* `policy_name` - (Required) Name of the policy to attach.
* `scope` - (Optional) Global scope. Only `"*"` is supported.
* `teams` - (Optional) Team aliases or patterns this attachment applies to.
* `keys` - (Optional) Key aliases or patterns this attachment applies to.
* `models` - (Optional) Model names or patterns this attachment applies to.
* `tags` - (Optional) Tag patterns this attachment applies to.

Attachment targeting must follow one of these forms:

* `scope = "*"` and none of `teams`, `keys`, `models`, `tags` are set
* `scope` unset and at least one of `teams`, `keys`, `models`, `tags` set

## Attribute Reference

* `id` - The attachment ID.
* `created_at` - When the attachment was created.
* `updated_at` - When the attachment was last updated.
* `created_by` - Who created the attachment.
* `updated_by` - Who last updated the attachment.

## Import

Policy attachments can be imported using their attachment ID:

```shell
terraform import litellm_policy_attachment.example <attachment-id>
```
