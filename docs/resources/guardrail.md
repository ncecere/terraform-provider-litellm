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
- `litellm_params` - (Sensitive String) A JSON-encoded object containing provider-specific parameters. This field stores only additional configuration specific to the guardrail provider (it does not include `guardrail`, `mode`, or `default_on`, which are top-level attributes). Objects are validated with exact JSON-number decoding, and semantically equivalent API responses preserve the configured Terraform spelling. Top-level API defaults are excluded during read-back. Within configured objects and arrays, null fields added by LiteLLM are ignored unless they were explicitly configured; non-null additions and missing or changed configured values remain visible as drift. LiteLLM-masked string leaves preserve the corresponding prior Terraform-owned plaintext, while visible sibling changes remain authoritative.
- `guardrail_info` - (String) A JSON-encoded object containing additional metadata or information about the guardrail. Objects are validated before any API request; semantically equivalent read-back preserves the configured Terraform spelling.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier for the guardrail (same as `guardrail_id`).
- `created_at` - Timestamp of when the guardrail was created.

## Import

Guardrails can be imported using their guardrail ID:

```shell
terraform import litellm_guardrail.example <guardrail-id>
```

LiteLLM v1.98 masks credential-bearing `litellm_params` on information reads. Terraform cannot recover plaintext that was never in prior state, so import fails safely when the remote object contains masked parameters. Recreate the guardrail under Terraform ownership or remove/rotate the sensitive remote parameter before importing; a redaction marker is never stored as if it were the credential.

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

## Security and API Behavior

- The `guardrail`, `mode`, and `default_on` fields are top-level attributes, not nested inside `litellm_params`.
- The `litellm_params` field is for provider-specific configuration only (for example, Bedrock identifiers and API keys).
- `Sensitive` hides values from ordinary Terraform output, but configured plaintext remains in Terraform state and plan files. Protect those artifacts and mark any enclosing outputs sensitive.
- LiteLLM v1.98 masks sensitive keys on GET/list responses as `*****` or a two-character-prefix, four-asterisk, two-character-suffix value. The provider recognizes only these exact markers; ordinary strings containing asterisks remain visible as drift.
- Guardrail reads and writes require authentication. LiteLLM v1.98 restricts create, update, and delete to `PROXY_ADMIN`; v2 list visibility is role/team filtered by LiteLLM. Guardrail CRUD itself is not Enterprise-license gated, but database-backed management requires LiteLLM database support.
- Multiple guardrails can be combined for defense in depth. Test guardrails thoroughly before enabling them in production.
