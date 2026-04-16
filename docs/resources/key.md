# litellm_key Resource

Manages a LiteLLM API key.

## Example Usage

### Minimal Key (auto-generated)

```hcl
resource "litellm_key" "minimal" {
}

output "key_value" {
  value     = litellm_key.minimal.key
  sensitive = true
}
```

### Predefined Key Value

You can supply your own key value instead of letting LiteLLM generate one:

```hcl
resource "litellm_key" "predefined" {
  key       = "sk-my-custom-key-value"
  key_alias = "my-custom-key"
  models    = ["gpt-4o"]
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

### Key with Complex Metadata (Logging Configuration)

```hcl
resource "litellm_key" "with_logging" {
  key_alias = "logged-key"

  metadata = {
    environment = "production"
    logging = jsonencode([
      {
        callback_name = "langsmith"
        callback_type = "success"
        callback_vars = {
          langsmith_project = "my-project"
        }
      }
    ])
  }
}
```

### Key with Router Settings

```hcl
resource "litellm_key" "with_router_settings" {
  key_alias = "router-configured-key"
  models    = ["gpt-4o", "gpt-4o-mini"]

  router_settings {
    num_retries   = 3
    timeout       = 30.0
    allowed_fails = 2
    cooldown_time = 60.0

    fallbacks {
      model           = "gpt-4o"
      fallback_models = ["gpt-4o-mini", "gpt-3.5-turbo"]
    }

    context_window_fallbacks {
      model           = "gpt-4o"
      fallback_models = ["gpt-4o-mini"]
    }

    retry_policy {
      rate_limit_error_retries     = 3
      timeout_error_retries        = 2
      internal_server_error_retries = 1
    }
  }
}
```

## Argument Reference

The following arguments are supported:

* `key` - (Optional) User-defined key value. If not set, LiteLLM generates a 16-digit unique `sk-` key automatically. The key is stored as a sensitive value in state.

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

* `metadata` - (Optional) Map of metadata associated with this key. Values are strings; use `jsonencode()` for complex values (objects, arrays) such as logging configuration — they will be sent as native JSON to the API.

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

* `router_settings` - (Optional) Per-key router configuration block. Overrides team and global router settings for requests made with this key. Supports the following nested arguments:
  * `routing_strategy` - (Optional) Strategy for routing requests (e.g. `"simple-shuffle"`, `"least-busy"`, `"latency-based-routing"`, `"cost-based-routing"`, `"usage-based-routing"`).
  * `num_retries` - (Optional) Number of retries on failure.
  * `timeout` - (Optional) Request timeout in seconds.
  * `stream_timeout` - (Optional) Timeout in seconds for streaming requests.
  * `max_fallbacks` - (Optional) Maximum number of fallbacks to attempt.
  * `allowed_fails` - (Optional) Number of failures allowed before a model is put into cooldown.
  * `cooldown_time` - (Optional) Seconds a model stays in cooldown after exceeding `allowed_fails`.
  * `retry_after` - (Optional) Minimum seconds to wait before retrying a rate-limited request.
  * `default_max_parallel_requests` - (Optional) Default maximum parallel requests per deployment.
  * `enable_pre_call_checks` - (Optional) Enable pre-call checks such as context window validation.
  * `set_verbose` - (Optional) Enable verbose logging for requests made with this key.
  * `enable_tag_filtering` - (Optional) Enable tag-based routing.
  * `tag_filtering_match_any` - (Optional) When tag filtering is enabled, match deployments with any request tag (`true`) vs all tags (`false`).
  * `disable_cooldowns` - (Optional) Disable the cooldown mechanism.
  * `routing_strategy_args` - (Optional) Map of additional arguments for the routing strategy.
  * `model_group_alias` - (Optional) Map of model group name aliases.
  * `default_litellm_params` - (Optional) Map of default parameters merged into every request.
  * `fallbacks` - (Optional) List of fallback chains for general failures. Each block requires:
    * `model` - (Required) The primary model name.
    * `fallback_models` - (Required) Ordered list of fallback model names.
  * `context_window_fallbacks` - (Optional) List of fallback chains triggered on context window errors. Same structure as `fallbacks`.
  * `content_policy_fallbacks` - (Optional) List of fallback chains triggered on content policy violations. Same structure as `fallbacks`.
  * `retry_policy` - (Optional) Per-error-type retry counts overriding `num_retries`. Supports:
    * `bad_request_error_retries` - (Optional) Retries for HTTP 400 errors.
    * `authentication_error_retries` - (Optional) Retries for HTTP 401 errors.
    * `timeout_error_retries` - (Optional) Retries for timeout errors.
    * `rate_limit_error_retries` - (Optional) Retries for HTTP 429 errors.
    * `content_policy_violation_error_retries` - (Optional) Retries for content policy violations.
    * `internal_server_error_retries` - (Optional) Retries for HTTP 500 errors.

* `blocked` - (Optional) Whether this key is blocked.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - Non-sensitive unique identifier for this key (SHA256 hash of the key value). This is safe to appear in logs and CI/CD output.

* `key` - The API key token (sensitive). This is the actual secret used for authentication.

## Import

LiteLLM keys can be imported using the raw key token:

```shell
$ terraform import litellm_key.example sk-xxxxxxxxxxxx
```

The provider will automatically hash the key for the resource ID and store the raw value in the sensitive `key` attribute.

## Upgrade Notes

### v1.1.0 → v1.2.0: Hashed Resource ID

Prior to v1.2.0, the resource `id` was set to the raw API key value, which meant the secret was exposed in plaintext in Terraform CLI output and CI/CD logs.

Starting in v1.2.0, the `id` is a SHA256 hash of the key (`sha256:...`). **This migration is automatic** — Terraform will silently upgrade your state on the next `plan` or `apply`. No manual action is required.
