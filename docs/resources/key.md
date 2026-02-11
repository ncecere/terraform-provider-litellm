# litellm_key Resource

Manages a LiteLLM API key.

## Example Usage

### Minimal Key

```hcl
resource "litellm_key" "minimal" {
}

output "key_value" {
  value     = litellm_key.minimal.key
  sensitive = true
}
```

### Key with Budget and Rate Limits

```hcl
resource "litellm_key" "example" {
  key_alias             = "prod-key-1"
  models                = ["gpt-4o", "gpt-4o-mini"]
  max_budget            = 100.0
  tpm_limit             = 50000
  rpm_limit             = 200
  tpm_limit_type        = "best_effort_throughput"
  rpm_limit_type        = "best_effort_throughput"
  budget_duration       = "30d"
  max_parallel_requests = 10
  soft_budget           = 80.0
  duration              = "90d"
  blocked               = false

  allowed_routes         = ["llm_api_routes"]
  allowed_cache_controls = ["no-cache"]

  metadata = {
    "environment" = "production"
    "owner"       = "terraform"
  }

  model_rpm_limit = {
    "gpt-4o" = 100
  }

  model_tpm_limit = {
    "gpt-4o" = 25000
  }
}
```

### Service Account Key

```hcl
resource "litellm_key" "service_account" {
  service_account_id = "github-ci"
  team_id            = "team456"

  # When team_id is set and models are omitted, the provider
  # automatically allows the key to call all team models.
  metadata = {
    "environment" = "automation"
  }

  allowed_routes = [
    "/chat/completions",
    "/keys/*"
  ]
}
```

## Argument Reference

The following arguments are supported:

* `key_alias` - (Optional) Human-readable alias for this key.

* `models` - (Optional) List of models that can be used with this key.

* `max_budget` - (Optional) Maximum budget for this key.

* `user_id` - (Optional) User ID associated with this key.

* `team_id` - (Optional) Team ID associated with this key. If set and `models` is omitted, the provider automatically allows the key to use all models that belong to the team by sending `"all-team-models"` to the API.

* `organization_id` - (Optional) Organization ID associated with this key.

* `budget_id` - (Optional) Budget ID to associate with this key.

* `service_account_id` - (Optional, ForceNew) Identifier for a team-owned service account. When set, the provider calls the service-account API and defaults `key_alias` to this value.

* `allowed_routes` - (Optional) List of LiteLLM proxy routes this key is allowed to call.

* `allowed_passthrough_routes` - (Optional) Pass-through endpoints the key is allowed to access.

* `max_parallel_requests` - (Optional) Maximum number of parallel requests allowed.

* `metadata` - (Optional) Map of string metadata associated with this key.

* `tpm_limit` - (Optional) Tokens per minute limit.

* `rpm_limit` - (Optional) Requests per minute limit.

* `tpm_limit_type` - (Optional) Type of TPM limit enforcement (e.g., `"best_effort_throughput"`, `"guaranteed_throughput"`).

* `rpm_limit_type` - (Optional) Type of RPM limit enforcement (e.g., `"best_effort_throughput"`, `"guaranteed_throughput"`).

* `budget_duration` - (Optional) Duration for the budget (e.g., `"30d"`, `"7d"`).

* `allowed_cache_controls` - (Optional) List of allowed cache control directives.

* `soft_budget` - (Optional) Soft budget warning threshold.

* `duration` - (Optional) Duration for which this key is valid (e.g., `"30d"`, `"90d"`).

* `aliases` - (Optional) Map of model aliases.

* `config` - (Optional) Map of configuration options.

* `permissions` - (Optional) Map of permissions.

* `model_max_budget` - (Optional) Map of maximum budget per model. **Note:** Requires LiteLLM Enterprise license.

* `model_rpm_limit` - (Optional) Map of requests per minute limit per model.

* `model_tpm_limit` - (Optional) Map of tokens per minute limit per model.

* `guardrails` - (Optional) List of guardrails applied to this key.

* `prompts` - (Optional) List of prompt IDs associated with this key.

* `enforced_params` - (Optional) List of enforced parameters for this key.

* `tags` - (Optional) List of tags. **Note:** Requires LiteLLM Enterprise license.

* `blocked` - (Optional) Whether this key is blocked.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The key token (same as `key`).

* `key` - The generated API key token (sensitive).

## Import

LiteLLM keys can be imported using the key token:

```shell
$ terraform import litellm_key.example sk-xxxxxxxxxxxx
```
