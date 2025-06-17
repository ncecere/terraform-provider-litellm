# LiteLLM Provider

The LiteLLM provider allows Terraform to manage LiteLLM resources. LiteLLM is a proxy service that standardizes the input/output across different LLM APIs.

## Example Usage

```hcl
terraform {
  required_providers {
    litellm = {
      source  = "ncecere/litellm"
      version = "~> 0.2.3"
    }
  }
}

provider "litellm" {
  api_base = "http://your-litellm-instance:4000"
  api_key  = "your-api-key"
}

# Example Model Configuration

```hcl
resource "litellm_model" "example" {
  model_name          = "gpt-4-proxy"
  custom_llm_provider = "openai"
  model_api_key       = var.openai_api_key
  model_api_base      = "https://api.openai.com/v1"
  base_model          = "gpt-4"
  tier                = "paid"
  mode                = "chat"
  reasoning_effort    = "medium"  # Optional: "low", "medium", or "high"
  
  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}
```

## Authentication

The LiteLLM provider requires an API key and base URL for authentication. These can be provided in the provider configuration block or via environment variables.

### Environment Variables

- `LITELLM_API_BASE` - The base URL of your LiteLLM instance
- `LITELLM_API_KEY` - Your LiteLLM API key

## Provider Arguments

The following arguments are supported in the provider block:

* `api_base` - (Required) The base URL of your LiteLLM instance. This can also be provided via the `LITELLM_API_BASE` environment variable.
* `api_key` - (Required) The API key used to authenticate with LiteLLM. This can also be provided via the `LITELLM_API_KEY` environment variable.
