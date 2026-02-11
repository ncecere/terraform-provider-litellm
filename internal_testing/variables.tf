variable "litellm_api_base" {
  description = "Base URL of the LiteLLM API (e.g. http://localhost:4000)"
  type        = string
  default     = "http://localhost:4000"
}

variable "litellm_api_key" {
  description = "API key for authenticating with LiteLLM"
  type        = string
  sensitive   = true
}
