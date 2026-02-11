# litellm_guardrail (Resource)

Manages guardrails in LiteLLM. Guardrails allow you to enforce content safety and validation policies on LLM requests and responses using various provider integrations.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_guardrail" "minimal" {
  guardrail_name = "content-safety"
  guardrail      = "aporia"
  mode           = "pre_call"
}
```

### Full Configuration

```hcl
resource "litellm_guardrail" "full" {
  guardrail_name = "bedrock-guardrail"
  guardrail      = "bedrock"
  mode           = "pre_call"
  default_on     = true

  litellm_params = jsonencode({
    "guardrailIdentifier" = "my-guardrail-id"
    "guardrailVersion"    = "1"
  })

  guardrail_info = jsonencode({
    "description" = "Production content safety guardrail"
  })
}
```

### Lakera Guardrail

```hcl
resource "litellm_guardrail" "lakera" {
  guardrail_name = "prompt-injection-detection"
  guardrail      = "lakera"
  mode           = "pre_call"
  default_on     = false
}
```

## Argument Reference

The following arguments are supported:

### Required

- `guardrail_name` - (String) A unique name for the guardrail.
- `guardrail` - (String) The guardrail provider to use. Supported values include `aporia`, `bedrock`, `lakera`, and others supported by LiteLLM.
- `mode` - (String) When the guardrail is applied. Must be one of:
  - `pre_call` - Validates input before the LLM request is sent.
  - `during_call` - Checks content during streaming responses.
  - `post_call` - Validates output after the LLM response is received.

### Optional

- `guardrail_id` - (String, ForceNew) The unique identifier for the guardrail. If not provided, one will be generated automatically. Changing this forces creation of a new resource.
- `default_on` - (Bool) Whether this guardrail is enabled by default for all requests.
- `litellm_params` - (String) A JSON-encoded string containing provider-specific parameters. This field stores only additional configuration specific to the guardrail provider (it does not include `guardrail`, `mode`, or `default_on`, which are top-level attributes). When reading back from the API, only the keys originally configured by the user are preserved, preventing the API's default values from appearing in state.
- `guardrail_info` - (String) A JSON-encoded string containing additional metadata or information about the guardrail.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier for the guardrail (same as `guardrail_id`).
- `created_at` - Timestamp of when the guardrail was created.

## Import

Guardrails can be imported using their guardrail ID:

```shell
terraform import litellm_guardrail.example <guardrail-id>
```

## Guardrail Modes

### pre_call

Validates input before sending to the LLM. Use for:

- Prompt injection detection
- Input sanitization
- PII detection in prompts

### during_call

Applied during streaming responses. Use for:

- Real-time content filtering
- Token-level safety checks

### post_call

Validates complete responses. Use for:

- Output content safety
- PII redaction
- Fact checking

## Notes

- The `guardrail`, `mode`, and `default_on` fields are top-level attributes, not nested inside `litellm_params`.
- The `litellm_params` field is for provider-specific configuration only (e.g., Bedrock guardrail identifiers, API keys).
- Multiple guardrails can be combined for defense in depth.
- Test guardrails thoroughly before enabling in production.
