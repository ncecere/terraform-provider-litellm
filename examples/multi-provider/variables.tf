# Variables for Multi-Provider Configuration

variable "litellm_api_base" {
  description = "Base URL for the LiteLLM API"
  type        = string
}

variable "litellm_api_key" {
  description = "API key for authenticating with LiteLLM"
  type        = string
  sensitive   = true
}

variable "openai_api_key" {
  description = "OpenAI API key"
  type        = string
  sensitive   = true
}

variable "anthropic_api_key" {
  description = "Anthropic API key"
  type        = string
  sensitive   = true
}

variable "azure_openai_key" {
  description = "Azure OpenAI API key"
  type        = string
  sensitive   = true
}

variable "azure_openai_endpoint" {
  description = "Azure OpenAI endpoint URL"
  type        = string
}

variable "aws_access_key_id" {
  description = "AWS Access Key ID for Bedrock"
  type        = string
  sensitive   = true
}

variable "aws_secret_access_key" {
  description = "AWS Secret Access Key for Bedrock"
  type        = string
  sensitive   = true
}

variable "aws_region" {
  description = "AWS Region for Bedrock"
  type        = string
  default     = "us-east-1"
}
