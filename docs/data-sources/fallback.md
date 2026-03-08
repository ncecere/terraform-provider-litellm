# litellm_fallback Data Source

Retrieves fallback configuration for a LiteLLM model by model name and fallback type.

## Example Usage

```hcl
data "litellm_fallback" "general" {
  model         = "gpt-3.5-turbo"
  fallback_type = "general"
}

output "fallback_models" {
  value = data.litellm_fallback.general.fallback_models
}
```

With default fallback type (general):

```hcl
data "litellm_fallback" "lookup" {
  model = "gpt-3.5-turbo"
}

output "id" {
  value = data.litellm_fallback.lookup.id
}
```

## Argument Reference

- `model` - (Required) The model name to get fallback configuration for.
- `fallback_type` - (Optional) Type of fallback. Defaults to `general`. One of `general`, `context_window`, or `content_policy`.

## Attribute Reference

- `id` - Unique identifier for this fallback (`model:fallback_type`).
- `model` - The model name (echo of the argument).
- `fallback_type` - The fallback type (echo of the argument or default).
- `fallback_models` - List of fallback model names in order of priority.

## Notes

- If no fallback is configured for the given model and type, the read will fail with an API error (e.g. 404). Ensure the fallback exists (e.g. created by a `litellm_fallback` resource) before using this data source.
