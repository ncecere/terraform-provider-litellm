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
    "gpt-4o" = 250.0
  })
}
```

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
* `budget_id` - (Optional) The budget ID to associate with this tag.
* `max_budget` - (Optional) Maximum budget (in USD) allowed for this tag.
* `soft_budget` - (Optional) Soft budget threshold (in USD). Triggers alerts but does not block requests.
* `max_parallel_requests` - (Optional) Maximum number of parallel requests allowed.
* `tpm_limit` - (Optional) Tokens per minute rate limit.
* `rpm_limit` - (Optional) Requests per minute rate limit.
* `budget_duration` - (Optional) Duration for the budget period (e.g., `"30d"`, `"7d"`, `"1h"`).
* `model_max_budget` - (Optional) JSON string specifying per-model maximum budgets. Use `jsonencode()` to set this value.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The identifier of the tag.

## Import

Tags can be imported using the tag name:

```shell
terraform import litellm_tag.example <tag-name>
```

## Notes

* Tag names must be unique within the LiteLLM instance.
* When `soft_budget` is exceeded, alerts are generated but requests continue to be served. When `max_budget` is exceeded, requests are blocked.
* The `budget_duration` resets the budget counter at the specified interval.
* Use `model_max_budget` with `jsonencode()` to set spending caps on individual models within the tag.
