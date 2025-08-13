# Example demonstrating the additional_litellm_params feature
# This example shows how to use custom parameters beyond the standard ones

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {
  api_base = "https://your-litellm-proxy.com"
  api_key  = var.litellm_api_key
}

# Example 1: Basic model with additional parameters
resource "litellm_model" "custom_openai_model" {
  model_name          = "my-custom-gpt-4"
  custom_llm_provider = "openai"
  base_model          = "gpt-4"
  
  # Standard parameters (existing functionality)
  tpm                = 1000
  rpm                = 100
  model_api_key      = var.openai_api_key
  tier               = "paid"
  
  # Additional custom parameters (addresses GitHub issue #11)
  additional_litellm_params = {
    "drop_params"              = "true"
    "timeout"                  = "30"
    "max_retries"             = "3"
    "organization"            = "my-org-id"
    "use_in_pass_through"     = "false"
    "max_file_size_mb"        = "10"
    "budget_duration"         = "1d"
    "mock_response"           = "This is a test response"
    "litellm_credential_name" = "my-openai-creds"
  }
}

# Example 2: AWS Bedrock model with custom parameters
resource "litellm_model" "bedrock_model" {
  model_name          = "my-claude-model"
  custom_llm_provider = "bedrock"
  base_model          = "anthropic.claude-3-sonnet-20240229-v1:0"
  
  # Standard AWS parameters
  aws_access_key_id     = var.aws_access_key_id
  aws_secret_access_key = var.aws_secret_access_key
  aws_session_name      = "litellm"
  aws_region_name       = "us-east-1"
  aws_role_name         = "arn:aws:iam::123456789000:role/bedrock"
  
  # Additional custom parameters for Bedrock
  additional_litellm_params = {
    "region_name"           = "us-east-1"
    "stream_timeout"        = "60"
    "max_budget"           = "100"
    "use_litellm_proxy"    = "true"
    "auto_router_config"   = "path/to/config.yaml"
  }
}

# Example 3: Vertex AI model with custom parameters
resource "litellm_model" "vertex_model" {
  model_name          = "my-gemini-model"
  custom_llm_provider = "vertex_ai"
  base_model          = "gemini-pro"
  
  # Standard Vertex parameters
  vertex_project     = var.vertex_project
  vertex_location    = var.vertex_location
  vertex_credentials = var.vertex_credentials
  
  # Additional custom parameters for Vertex AI
  additional_litellm_params = {
    "watsonx_region_name"     = "us-south"
    "litellm_trace_id"        = "trace-123"
    "configurable_clientside_auth_params" = "api_base,api_key"
    "auto_router_default_model" = "gemini-pro"
    "auto_router_embedding_model" = "text-embedding-004"
  }
}

# Variables
variable "litellm_api_key" {
  description = "API key for LiteLLM"
  type        = string
  sensitive   = true
}

variable "openai_api_key" {
  description = "OpenAI API key"
  type        = string
  sensitive   = true
}

variable "aws_access_key_id" {
  description = "AWS Access Key ID"
  type        = string
  sensitive   = true
}

variable "aws_secret_access_key" {
  description = "AWS Secret Access Key"
  type        = string
  sensitive   = true
}

variable "vertex_project" {
  description = "Google Cloud Project ID for Vertex AI"
  type        = string
  sensitive   = true
}

variable "vertex_location" {
  description = "Google Cloud location for Vertex AI"
  type        = string
  sensitive   = true
}

variable "vertex_credentials" {
  description = "Google Cloud credentials for Vertex AI"
  type        = string
  sensitive   = true
}
