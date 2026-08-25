# litellm_tag (Resource)

Manages a LiteLLM tag. Tags allow you to categorize and organize resources like models, teams, and API keys. Tags can also enforce budget limits, rate limits, and model-level spending controls.

## Example Usage

### Minimal Example

```hcl
resource "litellm_tag" "minimal" {
  name = "production"
}
```

### Tag with Description and Models

```hcl
resource "litellm_tag" "with_models" {
  name        = "chat-models"
  description = "Models used for chat applications"
  models      = ["gpt-4o", "gpt-4o-mini", "claude-sonnet-4-20250514"]
}
```

### Full Example with Budget and Rate Limits

```hcl
resource "litellm_tag" "full" {
  name                  = "enterprise-tier"
  description           = "Enterprise tier resources"
  models                = ["gpt-4o", "gpt-4o-mini"]
  max_budget            = 500.0
  soft_budget           = 400.0
  max_parallel_requests = 10
  tpm_limit             = 50000
  rpm_limit             = 500
  budget_duration       = "30d"
  model_max_budget = jsonencode({
    "gpt-4o" = {
      max_budget     = 250.0
      budget_duration = "30d"
    }
  })
}
```

> **License note:** LiteLLM v1.98 requires Enterprise for nonempty `model_max_budget` updates. Its create path does not apply the same gate, so validate create and later update behavior against your licensed deployment before adopting this field.

### Multiple Environment Tags

```hcl
resource "litellm_tag" "dev" {
  name           = "development"
  description    = "Development environment"
  models         = ["gpt-4o-mini"]
  max_budget     = 50.0
  rpm_limit      = 100
  budget_duration = "30d"
}

resource "litellm_tag" "prod" {
  name                  = "production"
  description           = "Production environment"
  models                = ["gpt-4o", "gpt-4o-mini"]
  max_budget            = 1000.0
  soft_budget           = 800.0
  max_parallel_requests = 20
  tpm_limit             = 100000
  rpm_limit             = 1000
  budget_duration       = "30d"
}
```

## Argument Reference

The following arguments are supported:

### Required

* `name` - (Required, ForceNew) Name of the tag. Must be unique. Changing this forces creation of a new resource.

### Optional

* `description` - (Optional) Description of the tag's purpose.
* `models` - (Optional, Computed) List of model names associated with this tag.
* `budget_id` - (Optional, Computed) Budget ID associated with this tag. Inline controls create a generated association. An existing association cannot be detached or reassigned safely in LiteLLM v1.98.
* `max_budget` - (Optional, Computed) Maximum budget (in USD) allowed for this tag.
* `soft_budget` - (Optional, Computed) Soft budget threshold (in USD). Triggers alerts but does not block requests.
* `max_parallel_requests` - (Optional, Computed) Maximum number of parallel requests allowed.
* `tpm_limit` - (Optional, Computed) Tokens per minute rate limit.
* `rpm_limit` - (Optional, Computed) Requests per minute rate limit.
* `budget_duration` - (Optional, Computed) Duration for the budget period (e.g., `"30d"`, `"7d"`, `"1h"`).
* `model_max_budget` - (Optional, Computed) JSON object mapping model names to LiteLLM `GenericBudgetConfig` objects. Supported nested fields are `max_budget`, `budget_duration`, `tpm_limit`, and `rpm_limit`; `budget_limit` and `time_period` are LiteLLM aliases. Use `jsonencode()`.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The identifier of the tag.

## Import

Tags can be imported using the tag name:

```shell
terraform import litellm_tag.example <tag-name>
```

Imports adopt authoritative nested budget values for visibility while recording each omitted budget control as API-owned. An unchanged configuration that omits those controls plans no mutation. Explicitly configuring an adopted value produces one harmless in-place ownership apply even when the value is equal; successful read-back transfers only that field.

Existing schema-v0 state has the same attribute types, ID, and import grammar. On upgrade, known budget fields without the new private provenance marker are conservatively treated like imported omissions. Explicit fields transfer ownership through one read-backed apply; omitted fields are not cleared.

## Budget Ownership and Clears

LiteLLM v1.98 returns tag budget values only through `litellm_budget_table`. Terraform reads that relation authoritatively and treats missing relations, null fields, and malformed values distinctly. Configured fields expose out-of-band drift. Fields omitted from an imported resource remain API-owned. Configure one explicitly and apply to transfer that field; owned numeric and duration fields can then be cleared, while the model-budget limitation below still applies.

Earlier provider documentation showed scalar per-model values. Finite numeric scalars remain readable, and unchanged configurations receive a compatibility warning. LiteLLM v1.98 rejects new or changed scalar values through budget update, so the provider blocks those transitions; migrate each scalar to a `GenericBudgetConfig` object. Do not combine `max_budget` with its `budget_limit` alias, or `budget_duration` with `time_period`, in the same model object.

Removing an owned numeric limit sends an explicit null through `/budget/update`. Removing `budget_duration` also clears `budget_reset_at`; configure numeric zero when zero is the intended limit.

LiteLLM v1.98 rejects both null and an empty object for `model_max_budget` through its tag and budget management APIs. The provider therefore rejects empty objects and removal of a known model budget before making an API call. Keep the existing value configured; clearing it requires direct database administration outside this API-only provider. Nonempty model-budget updates require LiteLLM Enterprise.

Do not combine `budget_id` with inline controls. A supplied budget can be shared by tags, keys, projects, or organizations; manage it with `litellm_budget` instead. Omitting an established `budget_id` preserves the association because v1.98 cannot safely detach it.

Inline controls can create a dedicated budget only during tag creation. LiteLLM v1.98 offers no atomic create-and-attach operation for an existing tag, so adding the first inline control later is rejected before mutation. Create a `litellm_budget` separately and attach only its `budget_id`. Once a tag has a verified association, configured sets and supported clears address that exact budget ID through `/budget/update`, preventing a concurrent reassociation from redirecting the mutation.

## Notes

* Tag names must be unique within the LiteLLM instance.
* When `soft_budget` is exceeded, alerts are generated but requests continue to be served. When `max_budget` is exceeded, requests are blocked.
* `budget_duration` initializes and updates LiteLLM's reset schedule.
* `/tag/new` can return an error after writing the tag, and a concurrent actor can create the same name between existence check and POST. If create ownership or configured read-back cannot be confirmed, a generated budget changes during reset initialization, or a dedicated-budget attachment cannot be confirmed, Terraform retains uncertainty-marked partial state and blocks refresh removal, update, and deletion. Verify ownership, remove that state entry without destroying the remote tag, and import explicitly to resume management.
* If create-time reset initialization fails, private pending state retains the original budget ID and duration and forces a later exact-ID retry even when public configuration is unchanged. Refresh and retry both fail closed if the tag is reassociated. Changed or removed duration configuration is rejected until the original retry and authoritative read-back succeed; only then is the marker removed.
* LiteLLM tag deletion removes the tag row but does not delete its budget row, historical spend, deployment tags, or process-local cache entries. A historically used name can remain visible as a dynamic item in `litellm_tags`.
* LiteLLM's tag management responses do not expose tag spend, so this resource cannot publish an authoritative `spend` attribute.
