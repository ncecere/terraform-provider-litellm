# Variables for Complete LiteLLM Configuration

# =============================================================================
# Provider Configuration
# =============================================================================

variable "litellm_api_base" {
  description = "Base URL for the LiteLLM API"
  type        = string
  default     = ""
}

variable "litellm_api_key" {
  description = "API key for authenticating with LiteLLM"
  type        = string
  sensitive   = true
  default     = ""
}

variable "insecure_skip_verify" {
  description = "Skip TLS certificate verification"
  type        = bool
  default     = false
}

# =============================================================================
# API Keys for Model Providers
# =============================================================================

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

# =============================================================================
# Integration API Keys
# =============================================================================

variable "github_token" {
  description = "GitHub personal access token for MCP server"
  type        = string
  sensitive   = true
  default     = ""
}

variable "tavily_api_key" {
  description = "Tavily API key for web search"
  type        = string
  sensitive   = true
  default     = ""
}
