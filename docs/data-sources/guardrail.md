# litellm_guardrail Data Source

Retrieves information about a specific LiteLLM guardrail configuration.

## Example Usage

```hcl
data "litellm_guardrail" "existing" {
  guardrail_id = "my-guardrail-id"
}

output "guardrail_info" {
  value = {
    name       = data.litellm_guardrail.existing.guardrail_name
    guardrail  = data.litellm_guardrail.existing.guardrail
    mode       = data.litellm_guardrail.existing.mode
    default_on = data.litellm_guardrail.existing.default_on
  }
}
```

## Argument Reference

* `guardrail_id` - (Required) The unique identifier of the guardrail to retrieve.

## Attribute Reference

* `id` - The unique identifier of the guardrail.
* `guardrail_id` - The guardrail ID.
* `guardrail_name` - Human-readable name for the guardrail.
* `guardrail` - The guardrail integration type (e.g., "aporia", "bedrock", "lakera").
* `mode` - When to apply the guardrail: a mode string, canonical JSON string array, or canonical JSON `Mode` object with tag-specific routing.
* `default_on` - Whether the guardrail is enabled by default for all requests.
* `litellm_params` - Sensitive canonical JSON object string containing additional provider-specific parameters. Empty additional parameters are represented as `{}` rather than null. LiteLLM v1.98 returns credential-bearing values as masked placeholders; this data source does not recover plaintext.
* `guardrail_info` - JSON string containing additional guardrail metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.

## Security and Access

Sensitivity propagates through Terraform expressions. Mark outputs that expose `litellm_params` as sensitive. Masked values are inventory metadata, not usable credentials, and configured plaintext is not available to a read-only data source.

LiteLLM requires authentication for guardrail reads. In v1.98, the single-info route does not apply the v2 list route's team/status filtering, so restrict provider credentials and guardrail-ID access accordingly. This data source does not bypass LiteLLM authorization or access its database directly.

Ordinary reads retry bounded transient transport, HTTP 408, 429, and 5xx failures. The data source accepts only LiteLLM's known `db` and `config` definition locations; malformed, identity-mismatched, or unknown-authority responses publish no partial state. A 404 remains an error for the data source.
