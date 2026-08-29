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

- `model` - (Required) The non-empty model name to get fallback configuration for. Supply the raw model identifier rather than a URL-encoded value; the provider escapes path characters exactly once.
- `fallback_type` - (Optional) Type of fallback. Defaults to `general`. One of `general`, `context_window`, or `content_policy`; the exact case-sensitive LiteLLM v1.98 query enum is validated during planning.

## Attribute Reference

- `id` - Unique identifier for this fallback (`model:fallback_type`).
- `model` - The model name (echo of the argument).
- `fallback_type` - The fallback type (echo of the argument or default).
- `fallback_models` - List of fallback model names in order of priority.

## Notes

- Model names containing colons, percent signs, query delimiters, Unicode, and other special characters retain their raw identity in state. LiteLLM must support that model identity on its fallback route.
- If no fallback is configured for the given model and type, the read will fail with a content-safe diagnostic. Ensure the fallback exists (e.g. created by a `litellm_fallback` resource) before using this data source; consult trusted proxy logs for server-side details.
