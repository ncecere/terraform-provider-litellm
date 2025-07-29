# Resource: litellm_model_config

Manages model configurations in LiteLLM for routing and parameter settings. This resource allows you to configure model-specific settings including parameters, rate limits, and capabilities.

## Example Usage

### Basic Model Configuration

```hcl
resource "litellm_model_config" "gemini_pro" {
  model_name = "gemini-1.5-pro"
  
  litellm_params = {
    model           = "vertex_ai/gemini-1.5-pro-001"
    vertex_project  = "my-gcp-project"
    vertex_location = "us-central1"
    max_tokens      = "8192"
    temperature     = "0.7"
  }
  
  model_info = {
    mode                      = "chat"
    base_model                = "gemini-1.5-pro"
    supports_function_calling = "true"
    supports_vision           = "true"
  }
}
```

### Advanced Configuration with Rate Limits

```hcl
resource "litellm_model_config" "gpt4_config" {
  model_name = "gpt-4-turbo"
  enabled    = true
  priority   = 10
  
  litellm_params = {
    model             = "gpt-4-turbo-preview"
    api_key           = var.openai_api_key
    max_tokens        = "4096"
    temperature       = "0.7"
    top_p             = "0.9"
    frequency_penalty = "0.0"
    presence_penalty  = "0.0"
  }
  
  model_info = {
    mode                      = "chat"
    supports_function_calling = "true"
    supports_vision           = "true"
    context_window            = "128000"
  }
  
  # Rate limiting
  rpm_limit = 1000  # Requests per minute
  tpm_limit = 90000 # Tokens per minute
}
```

### Load Balancing Configuration

```hcl
resource "litellm_model_config" "primary_model" {
  model_name = "primary-gpt4"
  priority   = 100  # Higher priority for primary model
  
  litellm_params = {
    model      = "gpt-4"
    api_key    = var.primary_api_key
    max_tokens = "4096"
  }
  
  rpm_limit = 500
  tpm_limit = 50000
}

resource "litellm_model_config" "fallback_model" {
  model_name = "fallback-gpt35"
  priority   = 50  # Lower priority for fallback
  
  litellm_params = {
    model      = "gpt-3.5-turbo"
    api_key    = var.fallback_api_key
    max_tokens = "4096"
  }
  
  rpm_limit = 2000
  tpm_limit = 200000
}
```

## Argument Reference

### Required Arguments

- `model_name` (String) - Name of the model configuration. This is how the model will be referenced in API calls.
- `litellm_params` (Map of String) - LiteLLM parameters for the model. Common parameters include:
  - `model` - The actual model identifier (e.g., "gpt-4", "vertex_ai/gemini-1.5-pro")
  - `api_key` - API key for the model provider (if applicable)
  - `max_tokens` - Maximum tokens for completion
  - `temperature` - Temperature setting for randomness
  - Provider-specific parameters (e.g., `vertex_project`, `vertex_location` for Google Vertex AI)

### Optional Arguments

- `model_info` (Map of String) - Additional model information and capabilities:
  - `mode` - Model mode (e.g., "chat", "completion", "embedding")
  - `base_model` - Base model name
  - `supports_function_calling` - Whether the model supports function calling
  - `supports_vision` - Whether the model supports image inputs
  - `context_window` - Size of the context window
- `enabled` (Boolean) - Whether this model configuration is enabled. Default: `true`.
- `priority` (Number) - Priority for model selection in load balancing (higher values preferred). Default: `0`.
- `rpm_limit` (Number) - Requests per minute limit for this model.
- `tpm_limit` (Number) - Tokens per minute limit for this model.

## Attributes Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier of the model configuration.

## Import

Model configurations can be imported using their ID:

```bash
terraform import litellm_model_config.example model-config-id
```

## Use Cases

### 1. Multi-Provider Setup

```hcl
# OpenAI Configuration
resource "litellm_model_config" "openai_gpt4" {
  model_name = "gpt-4"
  
  litellm_params = {
    model   = "gpt-4"
    api_key = var.openai_api_key
  }
}

# Anthropic Configuration
resource "litellm_model_config" "claude3" {
  model_name = "claude-3"
  
  litellm_params = {
    model   = "claude-3-opus-20240229"
    api_key = var.anthropic_api_key
  }
}

# Google Vertex AI Configuration
resource "litellm_model_config" "gemini" {
  model_name = "gemini-pro"
  
  litellm_params = {
    model           = "vertex_ai/gemini-1.5-pro"
    vertex_project  = var.gcp_project
    vertex_location = var.gcp_region
  }
}
```

### 2. Environment-Specific Configurations

```hcl
locals {
  environment = terraform.workspace
}

resource "litellm_model_config" "model" {
  model_name = "primary-model-${local.environment}"
  
  litellm_params = {
    model      = var.model_configs[local.environment].model
    api_key    = var.model_configs[local.environment].api_key
    max_tokens = var.model_configs[local.environment].max_tokens
  }
  
  # Different rate limits per environment
  rpm_limit = var.model_configs[local.environment].rpm_limit
  tpm_limit = var.model_configs[local.environment].tpm_limit
}
```

### 3. A/B Testing Configuration

```hcl
resource "litellm_model_config" "model_a" {
  model_name = "test-model-a"
  priority   = 50
  
  litellm_params = {
    model       = "gpt-4"
    temperature = "0.7"
  }
}

resource "litellm_model_config" "model_b" {
  model_name = "test-model-b"
  priority   = 50  # Equal priority for even distribution
  
  litellm_params = {
    model       = "gpt-4-turbo"
    temperature = "0.8"
  }
}
```

## Notes

- Model configurations are used by the LiteLLM router to determine which models to use and how to configure them.
- The `priority` field is used for load balancing - models with higher priority are preferred.
- Rate limits (`rpm_limit` and `tpm_limit`) help prevent exceeding provider quotas.
- Provider-specific parameters should be included in `litellm_params` according to the provider's requirements.