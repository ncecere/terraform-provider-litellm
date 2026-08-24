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

In write-only mode, `key` remains null and `id` contains only `sha256:<key-hash>`. The provider uses this hash for Read, Update, and Delete; plaintext is not stored in Terraform private state.

> **Key entropy:** Use a cryptographically random key with at least 128 bits of entropy. The unsalted SHA256 management identifier does not authenticate to LiteLLM, but it is visible in ordinary Terraform output and lets an observer verify offline guesses of a weak or patterned predefined key.

> **Compatibility:** The optional write-only argument requires Terraform or OpenTofu 1.11+. Older clients can continue using the provider when `key_wo` is omitted, but reject configurations that set it.

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

## Argument Reference

The following arguments are supported:

* `key` - (Optional) User-defined key value. If not set, LiteLLM generates a 16-digit unique `sk-` key automatically. The key is stored as a sensitive value in state. Conflicts with `key_wo`.

* `key_wo` - (Optional, Sensitive, Write-only) Predefined key sent to LiteLLM but represented only by null in plan and state. Must be configured with `key_wo_version`. Use a cryptographically random, high-entropy value because its SHA256 management identifier is persisted. Requires Terraform or OpenTofu 1.11+.

* `key_wo_version` - (Optional, ForceNew) Persisted version or nonce for `key_wo`. Change this whenever the write-only secret changes. It conflicts with configurations that omit `key_wo`.

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
