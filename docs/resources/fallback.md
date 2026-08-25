# litellm_fallback (Resource)

Manages fallback configuration for a LiteLLM model. Fallbacks define which models to try when a primary model call fails after retries. You can configure separate fallbacks for general errors, context-window exceeded, and content-policy violations.

## Example Usage

### Minimal (general fallback)

```hcl
resource "litellm_model" "primary" {
  model_name          = "gpt-3.5-turbo"
  custom_llm_provider = "openai"
  base_model          = "gpt-3.5-turbo"
}

resource "litellm_model" "fallback" {
  model_name          = "gpt-4o-mini"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_fallback" "general" {
  model           = litellm_model.primary.model_name
  fallback_models = [litellm_model.fallback.model_name]
  fallback_type   = "general"
}
```

### All fallback types

```hcl
resource "litellm_fallback" "general" {
  model           = "my-model"
  fallback_models = ["gpt-4o", "gpt-4o-mini"]
  fallback_type   = "general"
}

resource "litellm_fallback" "context_window" {
  model           = "my-model"
  fallback_models = ["gpt-4o"]
  fallback_type   = "context_window"
}

resource "litellm_fallback" "content_policy" {
  model           = "my-model"
  fallback_models = ["gpt-4o-mini"]
  fallback_type   = "content_policy"
}
```

## Argument Reference

### Required

- `model` - (String, Forces new resource) The model name to configure fallbacks for (e.g. `gpt-3.5-turbo`). Must match a model that exists on the proxy. A model cannot be its own fallback.
- `fallback_models` - (List of String) List of fallback model names in order of priority. Each must be a model known to the proxy.

### Optional

- `fallback_type` - (String, Optional, Forces new resource) Type of fallback. Defaults to `general`. One of:
  - `general` - Used for any error after retries.
  - `context_window` - Used when the request exceeds the model's context window.
  - `content_policy` - Used for content policy violations.

  The provider validates this exact, case-sensitive LiteLLM v1.98 request enum during planning.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

- `id` - Unique identifier for this fallback (`model:fallback_type`).

## Import

Fallbacks can be imported using the composite ID `model:fallback_type`:

```shell
terraform import litellm_fallback.example "gpt-3.5-turbo:general"
```

The provider recognizes the exact supported fallback-type suffix from the right, so colons in a model identifier are preserved:

```shell
terraform import litellm_fallback.example "llama3:8b:general"
```

The suffix is required and must be exactly `general`, `context_window`, or `content_policy`. The model component must not be empty. Pass the model identifier in its raw form; do not URL-encode `/`, `%`, `?`, Unicode, or other characters before constructing the import ID. The provider URL-escapes the model once when calling LiteLLM. Whether a particular identity is accepted is still subject to the LiteLLM proxy's model and route support.

## Notes

- Resource addresses, schema, state, and IDs remain unchanged: the state ID is the raw `model:fallback_type` value.
- The LiteLLM API allows one fallback configuration per `(model, fallback_type)` pair. Creating a resource with the same model and type updates the existing configuration.
- Fallback models must exist on the proxy and cannot include the primary model itself.
