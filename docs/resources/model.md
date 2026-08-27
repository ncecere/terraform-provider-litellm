# litellm_model Resource

Manages a LiteLLM model configuration. This resource allows you to create, update, and delete model configurations in your LiteLLM instance.

## Example Usage

### Basic OpenAI Model

```hcl
resource "litellm_model" "gpt4" {
  model_name          = "gpt-4-proxy"
  custom_llm_provider = "openai"
  model_api_key       = var.openai_api_key
  base_model          = "gpt-4"
  tier                = "paid"
  mode                = "chat"

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}
```

### Advanced Model with All Features

```hcl
resource "litellm_model" "advanced_gpt4" {
  model_name          = "gpt-4-advanced"
  custom_llm_provider = "openai"
  model_api_key       = var.openai_api_key
  model_api_base      = "https://api.openai.com/v1"
  api_version         = "2023-05-15"
  base_model          = "gpt-4"
  tier                = "paid"
  team_id             = "team-123"
  mode                = "chat"
  reasoning_effort    = "medium"
  thinking_enabled    = true
  thinking_budget_tokens = 1024
  merge_reasoning_content_in_choices = true
  tpm                 = 100000
  rpm                 = 1000

  # Cost configuration (per million tokens)
  input_cost_per_million_tokens  = 30.0    # $0.03 per 1k tokens = $30 per million
  output_cost_per_million_tokens = 60.0    # $0.06 per 1k tokens = $60 per million
}
```

### AWS Bedrock Model with Cross-Account Access

```hcl
resource "litellm_model" "bedrock_claude" {
  model_name          = "bedrock-claude-proxy"
  custom_llm_provider = "bedrock"
  base_model          = "anthropic.claude-3-sonnet-20240229-v1:0"
  tier                = "paid"
  mode                = "chat"

  # AWS configuration with cross-account access
  aws_access_key_id     = var.aws_access_key_id
  aws_secret_access_key = var.aws_secret_access_key
  aws_region_name       = "us-east-1"
  aws_session_name      = "litellm-cross-account-session"
  aws_role_name         = "arn:aws:iam::123456789012:role/LiteLLMCrossAccountRole"

  input_cost_per_million_tokens  = 3.0
  output_cost_per_million_tokens = 15.0
}
```

### Anthropic Model

```hcl
resource "litellm_model" "claude" {
  model_name          = "claude-proxy"
  custom_llm_provider = "anthropic"
  model_api_key       = var.anthropic_api_key
  base_model          = "claude-3-sonnet-20240229"
  tier                = "paid"
  mode                = "chat"

  input_cost_per_million_tokens  = 3.0
  output_cost_per_million_tokens = 15.0
}
```

### Azure OpenAI Model

```hcl
resource "litellm_model" "azure_gpt4" {
  model_name          = "azure-gpt4-proxy"
  custom_llm_provider = "azure"
  model_api_key       = var.azure_openai_key
  model_api_base      = var.azure_openai_endpoint
  api_version         = "2023-12-01-preview"
  base_model          = "gpt-4"
  tier                = "paid"
  mode                = "chat"

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}
```

### Model with Credential Reference

```hcl
# First, create a credential to store API keys securely
resource "litellm_credential" "openai_creds" {
  credential_name = "openai-production"
  credential_values = {
    "api_key" = var.openai_api_key
  }
}

# Then, reference the credential in your model
resource "litellm_model" "gpt4_with_credential" {
  model_name             = "gpt-4-with-cred"
  custom_llm_provider    = "openai"
  base_model             = "gpt-4"
  tier                   = "paid"
  mode                   = "chat"
  litellm_credential_name = litellm_credential.openai_creds.credential_name

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}
```

### Model with Access Groups

```hcl
# Create models and assign them to access groups
resource "litellm_model" "gpt4_premium" {
  model_name          = "gpt-4-premium"
  custom_llm_provider = "openai"
  model_api_key       = var.openai_api_key
  base_model          = "gpt-4"
  tier                = "paid"
  mode                = "chat"

  # Assign to access groups - teams/keys with these groups can use this model
  access_groups = ["premium-models", "gpt4-access"]

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}

# Teams can reference access group names in their models list
resource "litellm_team" "premium_team" {
  team_alias = "premium-users"
  models     = ["premium-models"]  # Access group name, grants access to all models in this group
}
```

## Argument Reference

The following arguments are supported:

* `model_name` - (Required) string. The name of the model configuration used to identify the model in API calls.

* `custom_llm_provider` - (Required) string. The LLM provider for this model (e.g., "openai", "anthropic", "azure", "bedrock").

* `model_api_key` - (Optional) string (Sensitive). The API key for the underlying model provider.

* `model_api_base` - (Optional) string. The base URL for the model provider's API.

* `api_version` - (Optional) string. The API version to use for the model provider.

* `base_model` - (Required) string. The actual model identifier from the provider (e.g., "gpt-4", "claude-2").

* `tier` - (Optional) string. The usage tier for this model. LiteLLM v1.98 accepts exactly `"free"` or `"paid"`; the provider validates the value during planning. Default: `"free"`.

* `team_id` - (Optional) string. Associate the model with a specific team. Changing or removing an owned team association replaces the model because LiteLLM v1.98 does not provide a reliable in-place detach operation.

* `access_groups` - (Optional) list(string). List of access groups this model belongs to. Teams and keys with access to these groups can use this model. See [LiteLLM Access Groups](https://docs.litellm.ai/docs/proxy/model_access_groups) for more details.

* `mode` - (Optional) string. The intended use of the model. Removing an owned mode replaces the model because LiteLLM may infer and retain a mode during updates. LiteLLM v1.98 keeps this request field extensible rather than declaring an endpoint enum; common values include:
  * `chat`
  * `completion`
  * `embedding`
  * `audio_speech`
  * `audio_transcription`
  * `image_generation`
  * `video_generation`
  * `batch`
  * `rerank`
  * `realtime`
  * `responses`
  * `ocr`
  * `moderation`

* `tpm` - (Optional) integer. Tokens per minute limit for this model. Zero is a valid configured limit; it does not mean "unset." Removing an owned value replaces the model.

* `rpm` - (Optional) integer. Requests per minute limit for this model. Zero is a valid configured limit; it does not mean "unset." Removing an owned value replaces the model.

* `reasoning_effort` - (Optional) string. Configures the provider-specific reasoning effort level. Common values are:
  * `low`
  * `medium`
  * `high`

* `thinking_enabled` - (Optional) boolean. Enables the model's thinking capability. Default: `false`.

* `thinking_budget_tokens` - (Optional) integer. Sets the token budget for the model's thinking capability. Default: `1024`. Note: this field is only relevant when `thinking_enabled = true`.

* `merge_reasoning_content_in_choices` - (Optional) boolean. When set to `true`, merges reasoning content into the model's choices.

* `input_cost_per_million_tokens` - (Optional) float. Cost per million input tokens. The provider converts this to a per-token cost sent to the API.

* `output_cost_per_million_tokens` - (Optional) float. Cost per million output tokens. The provider converts this to a per-token cost sent to the API.

* `input_cost_per_pixel` - (Optional) float. Cost applied per input pixel for models that charge by image size. Removing an owned value replaces the model.

* `output_cost_per_pixel` - (Optional) float. Cost applied per output pixel for image-generation models. Removing an owned value replaces the model.

* `input_cost_per_second` - (Optional) float. Cost applied per input second for audio/transcription models. Removing an owned value replaces the model.

* `output_cost_per_second` - (Optional) float. Cost applied per output second for audio/transcription models. Removing an owned value replaces the model.

* `vertex_project` - (Optional) string. Vertex AI project id (for `custom_llm_provider = "vertex"`).

* `vertex_location` - (Optional) string. Vertex AI location (e.g., `us-central1`).

* `vertex_credentials` - (Optional) string. Vertex credentials (JSON string or path depending on your setup).

* `litellm_credential_name` - (Optional) string. Name of a credential created via `litellm_credential` resource. This allows you to reference stored credentials instead of providing API keys directly in the model configuration.

* `additional_litellm_params` - (Optional) map(string). A map of arbitrary additional parameters that will be merged into the `litellm_params` object sent to the LiteLLM API. This is intended for provider-specific or experimental options not exposed as dedicated arguments.

  Conversion and behavior rules (how the provider handles values):
  * When values in the map are strings the provider will attempt to coerce them:
    * `"true"` / `"false"` (strings) -> boolean true / false
    * Numeric strings are parsed first as integers; if integer parsing fails, parsed as floats (e.g., `"16384"` -> 16384, `"0.75"` -> 0.75)
    * JSON strings (starting with `[` or `{`) are parsed as JSON objects/arrays
    * Non-convertible strings remain strings
  * Non-string map values (if supplied) are passed through unchanged.
  * The provider merges these keys into the `litellm_params` payload sent to the API.
  * Note: the remote API may not echo back all custom parameters; this provider preserves `additional_litellm_params` in state when present in configuration.
  * Adding keys or changing values uses an in-place model update.
  * **Removing a key, clearing the map, or removing the argument replaces the model.** LiteLLM's update endpoints merge or retain some parameter classes instead of deleting them reliably. Terraform therefore plans replacement so the new model is created without the removed values rather than silently leaving stale remote configuration.
  * Imported remote parameters are adopted when `additional_litellm_params` remains omitted. Set the map explicitly, including `{}` to clear all imported parameters, when Terraform should take ownership of that imported parameter set. The first explicit configuration records that ownership in state even when its values already match the imported values.

  **Special parameter: `additional_drop_params`**
  * When `additional_drop_params` is provided as a JSON array string, it specifies parameters to remove from the final `litellm_params` before sending to the API
  * This allows you to override or remove built-in parameters if needed
  * The `additional_drop_params` key itself is not included in the final parameters

  Example showing booleans, integers, floats, strings, and parameter dropping:

  ```hcl
  resource "litellm_model" "with_additional" {
    model_name          = "custom-model"
    custom_llm_provider = "openai"
    model_api_key       = var.openai_api_key
    base_model          = "gpt-4"
    mode                = "chat"

    additional_litellm_params = {
      "use_fine_tune"          = "true"                    # becomes boolean true
      "max_context"            = "16384"                   # becomes integer 16384
      "scale"                  = "0.75"                    # becomes float 0.75
      "note"                   = "for testing"             # stays string
      "complex_config"         = "{\"nested\": {\"value\": 42}}"  # parsed as JSON object
      "additional_drop_params" = "[\"reasoningEffort\"]"   # removes reasoningEffort parameter
    }
  }
  ```

* `additional_model_info` - (Optional) map(string). A map of arbitrary additional fields that will be merged into the `model_info` object sent to the LiteLLM API. The main use case is declaring capability flags (`supports_vision`, `supports_function_calling`, `supports_reasoning`, `supports_response_schema`, …) for models that are missing from LiteLLM's model cost map, so that `/model/info` and `/v1/models` advertise the model's capabilities to clients.

  * Values follow the same string-to-native conversion rules as `additional_litellm_params` (booleans, integers, floats, JSON).
  * Only keys configured here are managed. LiteLLM merges metadata derived from its model cost map (`max_tokens`, `supports_*`, `litellm_provider`, …) into `/model/info` responses; those derived fields are ignored on read so they never appear as drift, and they are not captured on import.
  * Adding keys or changing values uses an in-place model update.
  * **Removing a key, clearing the map, or removing the argument replaces the model.** LiteLLM merges `model_info` during updates, so replacement ensures removed capability metadata is not silently retained.
  * LiteLLM fields managed by dedicated resource arguments (`base_model`, `tier`, `mode`, `team_id`, `access_groups`), internal identity fields, and system-managed audit fields are rejected as reserved keys.
  * Imported and cost-map-derived metadata is not adopted into this map. Set `additional_model_info` explicitly when Terraform should manage selected fields.

  ```hcl
  resource "litellm_model" "kimi_k3" {
    model_name          = "kimi-k3"
    custom_llm_provider = "openrouter"
    base_model          = "moonshotai/kimi-k3"
    mode                = "chat"

    additional_model_info = {
      "supports_vision"           = "true"  # becomes boolean true
      "supports_function_calling" = "true"
    }
  }
  ```

* `additional_model_info_json` - (Optional, Computed, Sensitive) a lossless JSON-object sibling for heterogeneous custom `model_info` values. Use this attribute when string coercion in `additional_model_info` cannot preserve the intended type.

  * The root must be one non-null JSON object with unique members. Nested objects, arrays, strings, booleans, numbers, and nested JSON null values are preserved without provider-side `float64` or string coercion. LiteLLM v1.98 omits arbitrary top-level null members when serializing `ModelInfo`, so the provider rejects them before any request; place a null inside a nested object or array when its presence is significant. Integers remain exact. Decimal/exponent values must survive LiteLLM v1.98's Python-float request/persistence round trip exactly; lossy values such as `1.0000000000000001` are rejected before any request instead of causing perpetual drift.
  * Top-level keys must be disjoint from `additional_model_info` and from fields managed by dedicated model attributes, including LiteLLM's mirrored `input_cost_per_token` and `output_cost_per_token` fields. Overlap is rejected before any request, without including keys or values in diagnostics.
  * Terraform manages only recursively owned JSON paths. Cost-map-derived and other API-only `model_info` fields are not adopted on read or import.
  * `{}` is an explicitly managed empty view and differs from an omitted attribute. Imports and states upgraded from an earlier provider keep this attribute null and unmanaged.
  * Any semantic value change, nested removal, clear, or removal of the attribute replaces the model. Formatting-only changes do not mutate the API, and semantically equal readback preserves the configured spelling.
  * Literal strings such as `"****"` remain observable values; `model_info` does not apply a credential-mask heuristic.
  * This attribute is sensitive because arbitrary custom metadata can contain confidential values. Mark any outputs derived from it as sensitive.

  ```hcl
  resource "litellm_model" "typed_metadata" {
    model_name          = "typed-metadata"
    custom_llm_provider = "openai"
    base_model          = "gpt-4o-mini"

    additional_model_info = {
      owner = "platform" # disjoint legacy string-map key
    }

    additional_model_info_json = jsonencode({
      native_false = false
      large_number = 9007199254740993
      nested = {
        nullable = null
        items    = [1, true, "1"]
      }
    })
  }
  ```

  `additional_litellm_params_json`, object-form Vertex credentials, and model-budget JSON are separate lifecycle surfaces and are not provided by this attribute.

### AWS-specific Configuration

* `aws_access_key_id` - (Optional) string (Sensitive). AWS access key ID for AWS-based models.

* `aws_secret_access_key` - (Optional) string (Sensitive). AWS secret access key for AWS-based models.

* `aws_region_name` - (Optional) string. AWS region name for AWS-based models.

* `aws_session_name` - (Optional) string (Sensitive). AWS session name for cross-account access scenarios.

* `aws_role_name` - (Optional) string (Sensitive). AWS IAM role name for cross-account access scenarios.

## Clear and Replacement Behavior

LiteLLM v1.98 merges model updates, so Terraform distinguishes fields with a verified clear representation from fields that cannot be removed safely.

The required `model_name`, `custom_llm_provider`, and `base_model` arguments can be changed in place but cannot be omitted. Setting `tier` back to its default (`"free"`) and setting a new non-empty `mode` are also in-place updates.

Removing an owned value clears or resets these fields in place:

* Provider connection fields: `model_api_key`, `model_api_base`, and `api_version`
* AWS, Vertex AI, and `litellm_credential_name` fields
* `reasoning_effort`, `thinking_enabled`, `thinking_budget_tokens`, and `merge_reasoning_content_in_choices`; removing the thinking arguments disables thinking and restores the state default of 1024 budget tokens
* `access_groups`
* `input_cost_per_million_tokens` and `output_cost_per_million_tokens`

Terraform verifies LiteLLM's authoritative update response before committing cleared state. Because the update response contains encrypted-at-rest strings, string and secret clears additionally require a decrypted fresh-worker read; ciphertext or a masked secret is not accepted as proof of removal. If LiteLLM workers temporarily retain an older cached model, Terraform reports `Model Clear Readback Not Yet Consistent` and retains prior state. Retry after worker caches converge.

Removing `tpm`, `rpm`, `mode`, per-pixel costs, or per-second costs replaces the model. Changing or removing `team_id` also replaces it. Removing keys from either additional map follows the replacement rules documented with those arguments. Replacement is intentional: sending `null` or omission for these fields can leave the previous remote value active. Review plans carefully when the model ID is referenced elsewhere.

Imported optional fields remain unmanaged while their arguments are omitted, preventing an import-only configuration from clearing or replacing remote settings. Explicitly configuring an imported field transfers ownership to Terraform, even when the configured value already matches LiteLLM. A later removal then follows the in-place clear or replacement behavior above.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - The ID of the model configuration.

## Import

Model configurations can be imported using the model ID:

```shell
terraform import litellm_model.gpt4 <model-id>
```

The model ID is generated when the model is created and is different from `model_name`. Imported optional values are preserved while omitted from configuration. See [Clear and Replacement Behavior](#clear-and-replacement-behavior) before taking ownership of an imported value.

## Security Note

Sensitive arguments are redacted in Terraform output, but values supplied directly can still be stored in Terraform state. Protect state and plan artifacts, restrict backend access, and prefer `litellm_credential_name` with a separately managed credential instead of hardcoding provider credentials. The provider preserves configured sensitive values when LiteLLM returns a recognized mask, but never treats a masked response as proof that a requested clear succeeded.
