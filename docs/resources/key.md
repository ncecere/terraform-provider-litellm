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

### Write-Only Predefined Key

Terraform or OpenTofu 1.11 and later can send a predefined key without storing it in plan or state through `key_wo`. For end-to-end protection, source the value from an ephemeral resource or variable; a non-ephemeral source can still persist the secret independently of this resource.

```hcl
variable "litellm_key" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "litellm_key" "write_only" {
  key_wo         = var.litellm_key
  key_wo_version = "1"
  key_alias      = "prod-key-1"
  models         = ["gpt-4o"]
}
```

`key_wo_version` is persisted because Terraform cannot compare write-only values. Change both the secret and version to replace the key. Replacement deletes and recreates the LiteLLM key, including its server-side spend history and budget window. The ephemeral source must be available during both plan and apply; applying a saved plan requires supplying the ephemeral input again.

In write-only mode, `key` remains null and `id` contains only `sha256:<key-hash>`. The provider uses this hash for Read, Update, and Delete; plaintext is not stored in Terraform private state. The same `id` can be passed to the `litellm_key` data source's `key_hash` argument for hash-only lookup.

> **Key entropy:** Use a cryptographically random key with at least 128 bits of entropy. The unsalted SHA256 management identifier does not authenticate to LiteLLM, but it is visible in ordinary Terraform output and lets an observer verify offline guesses of a weak or patterned predefined key.

> **Compatibility:** The optional write-only argument requires Terraform or OpenTofu 1.11+. Older clients can continue using the provider when `key_wo` is omitted, but reject configurations that set it.

### Create a Key and Email Its User

```hcl
resource "litellm_user" "recipient" {
  user_email      = "person@example.com"
  auto_create_key = false
}

resource "litellm_key" "invited" {
  user_id           = litellm_user.recipient.id
  key_alias         = "emailed-key"
  send_invite_email = true
}
```

`send_invite_email` is a write-only, create-only action flag. Before creating the key, the provider verifies that `user_id` resolves to that exact LiteLLM user and a syntactically valid, non-empty email address. LiteLLM then queues email processing after successful key creation, but returns before delivery and provides no delivery acknowledgement. Configure a supported [LiteLLM email backend](https://docs.litellm.ai/docs/proxy/email) before enabling it. Service-account keys cannot use this action.

LiteLLM v1.98.0's enterprise email implementation defaults `EMAIL_INCLUDE_API_KEY` to `true`, including raw generated or predefined `key_wo` values in email. Set `EMAIL_INCLUDE_API_KEY=false` unless email infrastructure and recipient mailboxes are approved secret-delivery channels.

The action is requested once per successful LiteLLM Create request, not once for the lifetime of the HCL configuration. Resource replacement requests another email. If the create response is lost before Terraform persists state, retrying can create another key and request another email; inspect LiteLLM before retrying an ambiguous failure.

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

### Key-Specific Router Settings

`router_settings` is a complete key-level override. When present, it takes precedence over team router settings; Terraform owns and replaces the complete document. Ordered or heterogeneous fields use `jsonencode()` so their JSON semantics round-trip without losing order or object-valued aliases.

```hcl
resource "litellm_key" "routed" {
  key_alias = "routed-key"
  models    = ["gpt-4o", "gpt-4o-mini"]

  router_settings = {
    routing_strategy = "usage-based-routing-v2"
    num_retries      = 3
    timeout          = 30.5
    retry_after      = 0.25

    fallbacks = jsonencode([
      { "gpt-4o" = ["gpt-4o-mini"] },
      { "*" = ["emergency-model"] }
    ])

    model_group_alias = jsonencode({
      fast = "gpt-4o-mini"
      primary = {
        model  = "gpt-4o"
        hidden = true
      }
    })

    retry_policy = {
      rate_limit_error_retries      = 5
      internal_server_error_retries = 2
    }
  }
}
```

Removing the entire block sends an explicit empty object to clear the key-level override and restores team/global inheritance. Setting `fallbacks = jsonencode([])` retains a non-empty key-level settings document and intentionally suppresses inherited fallbacks.

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

## Argument Reference

The following arguments are supported:

* `key` - (Optional) User-defined key value. If not set, LiteLLM generates a 16-digit unique `sk-` key automatically. The key is stored as a sensitive value in state. Conflicts with `key_wo`.

* `key_wo` - (Optional, Sensitive, Write-only) Predefined key sent to LiteLLM but represented only by null in plan and state. Must be configured with `key_wo_version`. Use a cryptographically random, high-entropy value because its SHA256 management identifier is persisted. Requires Terraform or OpenTofu 1.11+.

* `key_wo_version` - (Optional, ForceNew) Persisted version or nonce for `key_wo`. Change this whenever the write-only secret changes. It conflicts with configurations that omit `key_wo`.

* `send_invite_email` - (Optional, Write-only) Requests an asynchronous email for each successful key Create request. Requires `user_id` for an exact existing user with a syntactically valid email address and cannot be used with `service_account_id`. It is never persisted, read back, imported, or sent during Update; a successful apply does not confirm email delivery. Replacement or an ambiguous Create retry can request another email. Requires Terraform or compatible OpenTofu 1.11+ when configured.

* `key_alias` - (Optional) Human-readable alias for this key.

* `models` - (Optional) List of models that can be used with this key.

* `max_budget` - (Optional) Maximum budget for this key.

* `user_id` - (Optional) User ID associated with this key.

* `team_id` - (Optional) Team ID associated with this key. If set and `models` is omitted, the provider automatically allows the key to use all models that belong to the team by sending `"all-team-models"` to the API.

* `organization_id` - (Optional) Organization ID associated with this key.

* `project_id` - (Optional) Project ID associated with this key. When set, models and budget are validated against the project's limits.

* `budget_id` - (Optional) Budget ID to associate with this key.

* `service_account_id` - (Optional, ForceNew) Identifier for a team-owned service account. When set, the provider calls the service-account API and defaults `key_alias` to this value.

* `allowed_routes` - (Optional) List of LiteLLM proxy routes this key is allowed to call.

* `allowed_passthrough_routes` - (Optional) Pass-through endpoints the key is allowed to access.

* `max_parallel_requests` - (Optional) Maximum number of parallel requests allowed.

* `metadata` - (Optional, Sensitive) Map of metadata associated with this key. Values are strings; use `jsonencode()` for complex values (objects, arrays) such as logging configuration — they will be sent as native JSON to the API. The entire map is marked sensitive because metadata commonly contains credentials; Terraform redacts it from normal CLI output while retaining it in state. When LiteLLM returns nested credentials as `litellm_enc::` or another recognized redaction marker, the provider retains the corresponding configured leaf while continuing to refresh unmasked metadata values for drift.

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

* `router_settings` - (Optional) Complete key-specific router-settings document. Omitting the block leaves remote settings unmanaged. A configured block replaces the complete document on update rather than merging individual fields. Supported LiteLLM v1.98.0 fields:
  * `routing_strategy_args` - (Optional) JSON object passed to the routing strategy.
  * `routing_strategy` - (Optional) Routing strategy name.
  * `routing_groups` - (Optional) JSON array of routing groups.
  * `retry_policy` - (Optional) Typed retry counts: `bad_request_error_retries`, `authentication_error_retries`, `timeout_error_retries`, `rate_limit_error_retries`, `content_policy_violation_error_retries`, and `internal_server_error_retries`.
  * `model_group_retry_policy` - (Optional) JSON object mapping model groups to retry policies. Nested retry keys use LiteLLM's PascalCase names, such as `RateLimitErrorRetries`.
  * `model_group_affinity_config` - (Optional) JSON object mapping affinity groups to lists of model groups.
  * `allowed_fails` - (Optional) Failures allowed before cooldown.
  * `cooldown_time` - (Optional) Cooldown duration in seconds.
  * `num_retries` - (Optional) Number of request retries.
  * `timeout` - (Optional) Request timeout in seconds.
  * `max_retries` - (Optional) Maximum retries.
  * `retry_after` - (Optional) Retry delay in seconds; decimal values are supported.
  * `fallbacks` - (Optional) Ordered JSON array of model fallback mappings.
  * `context_window_fallbacks` - (Optional) Ordered JSON array of context-window fallback mappings.
  * `model_group_alias` - (Optional) JSON object mapping aliases to model groups or alias configuration objects.
  * `enable_tag_filtering` - (Optional) Enables request-tag routing.
  * `tag_routing_prefix` - (Optional) Prefix for tag-based routing.

  LiteLLM v1.98.0 accepts and stores all fields above, but its per-key request path currently applies only `fallbacks`, `context_window_fallbacks`, `num_retries`, `timeout`, `model_group_retry_policy`, `routing_strategy`, `enable_tag_filtering`, and `model_group_alias`. Other accepted fields are exposed for API fidelity and future LiteLLM behavior.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - Non-sensitive unique management identifier for this key (SHA256 hash of the key value). LiteLLM prevents this digest from authenticating as a virtual key. It is displayed in logs and CI/CD output, so low-entropy predefined keys remain susceptible to offline guessing.

* `key` - The API key token (sensitive). This is the actual secret used for authentication for generated or stateful predefined keys. It remains null in write-only mode.

## Import

LiteLLM keys can be imported using the raw key token:

```shell
$ terraform import litellm_key.example sk-xxxxxxxxxxxx
```

The provider will automatically hash the key for the resource ID and store the raw value in the sensitive `key` attribute.

To import without storing the raw key, configure `key_wo` with `key_wo_version = "1"`, calculate the SHA256 hash outside Terraform, and import the prefixed hash:

```shell
TF_VAR_litellm_key="$LITELLM_KEY" terraform import \
  litellm_key.example "sha256:<64-character-sha256>"
```

Hash import initializes `key_wo_version` to `"1"`; the configuration must initially match it. The ephemeral input must be available during import and subsequent operations. An initial apply may be needed to normalize other Optional+Computed key attributes.

Switching an existing stateful `key` resource to `key_wo` replaces the key and removes plaintext from the current state. Historical local backups or remote-state versions may still contain the old value and must be expired or purged according to the backend's retention controls.

## Upgrade Notes

### v1.1.0 → v1.2.0: Hashed Resource ID

Prior to v1.2.0, the resource `id` was set to the raw API key value, which meant the secret was exposed in plaintext in Terraform CLI output and CI/CD logs.

Starting in v1.2.0, the `id` is a SHA256 hash of the key (`sha256:...`). **This migration is automatic** — Terraform will silently upgrade your state on the next `plan` or `apply`. No manual action is required.
