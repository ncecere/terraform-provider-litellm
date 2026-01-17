# litellm_guardrail Resource

Manages a LiteLLM guardrail. Guardrails provide content filtering, safety checks, and policy enforcement for LLM interactions.

## Example Usage

### Minimal Example

```hcl
resource "litellm_guardrail" "basic" {
  guardrail_name = "basic-safety"
  litellm_params = jsonencode({
    guardrail = "aporia"
    mode      = "post_call"
  })
}
```

### Full Example with Multiple Guardrails

```hcl
resource "litellm_guardrail" "content_safety" {
  guardrail_name = "content-safety-filter"
  
  litellm_params = jsonencode({
    guardrail         = "bedrock"
    guardrail_id      = "abc123"
    guardrail_version = "1"
    mode              = "during_call"
    
    content_filters = {
      hate_speech    = "block"
      violence       = "block"
      sexual_content = "block"
      profanity      = "warn"
    }
  })
  
  description = "Enterprise content safety guardrail"
  
  metadata = jsonencode({
    compliance  = "SOC2"
    review_date = "2024-01-15"
  })
}
```

### Aporia Guardrail

```hcl
resource "litellm_guardrail" "aporia_safety" {
  guardrail_name = "aporia-guardrail"
  
  litellm_params = jsonencode({
    guardrail    = "aporia"
    mode         = "post_call"
    api_key      = var.aporia_api_key
    api_base     = "https://api.aporia.com"
    
    policies = [
      "no_pii",
      "no_harmful_content",
      "factual_accuracy"
    ]
  })
}
```

### AWS Bedrock Guardrail

```hcl
resource "litellm_guardrail" "bedrock_guardrail" {
  guardrail_name = "bedrock-safety"
  
  litellm_params = jsonencode({
    guardrail         = "bedrock"
    guardrail_id      = "gr-xxxxxxxxxx"
    guardrail_version = "DRAFT"
    mode              = "during_call"
    
    aws_region = "us-east-1"
  })
}
```

### LakeraGuard Integration

```hcl
resource "litellm_guardrail" "lakera_guard" {
  guardrail_name = "lakera-protection"
  
  litellm_params = jsonencode({
    guardrail = "lakera"
    mode      = "pre_call"
    api_key   = var.lakera_api_key
    
    categories = [
      "prompt_injection",
      "jailbreak",
      "pii_detection"
    ]
  })
}
```

### Multiple Guardrail Stack

```hcl
# Pre-call guardrail for input validation
resource "litellm_guardrail" "input_filter" {
  guardrail_name = "input-validation"
  
  litellm_params = jsonencode({
    guardrail = "custom"
    mode      = "pre_call"
    
    rules = {
      max_input_length = 4000
      block_patterns   = ["ignore previous", "system prompt"]
    }
  })
}

# Post-call guardrail for output safety
resource "litellm_guardrail" "output_filter" {
  guardrail_name = "output-safety"
  
  litellm_params = jsonencode({
    guardrail = "aporia"
    mode      = "post_call"
    
    check_pii        = true
    check_toxicity   = true
    redact_sensitive = true
  })
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `guardrail_name` - (Required) Unique name for the guardrail.
* `litellm_params` - (Required) JSON string containing guardrail configuration parameters.

### Optional Arguments

* `description` - (Optional) Description of the guardrail's purpose.
* `metadata` - (Optional) JSON string containing additional metadata.

### litellm_params Configuration

The `litellm_params` JSON object supports the following common fields:

* `guardrail` - The guardrail provider. Valid values: `aporia`, `bedrock`, `lakera`, `custom`.
* `mode` - When to apply the guardrail. Valid values: `pre_call`, `during_call`, `post_call`.
* `api_key` - API key for third-party guardrail services.
* `api_base` - Base URL for guardrail API.

Provider-specific fields vary based on the guardrail type selected.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this guardrail.
* `guardrail_id` - The guardrail ID (same as id).
* `created_at` - Timestamp when the guardrail was created.
* `updated_at` - Timestamp when the guardrail was last updated.

## Import

Guardrails can be imported using the guardrail name:

```shell
terraform import litellm_guardrail.example content-safety-filter
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

- Multiple guardrails can be combined for defense in depth
- Guardrail order matters - pre_call runs before during_call and post_call
- API keys for guardrail services should be stored securely
- Test guardrails thoroughly before enabling in production
- Monitor guardrail metrics to tune sensitivity
