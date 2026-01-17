# Minimal LiteLLM Provider Configuration
# This example shows the simplest possible setup

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

# Provider configuration using environment variables
# Set LITELLM_API_BASE and LITELLM_API_KEY before running
provider "litellm" {}

# Create a basic model
resource "litellm_model" "basic" {
  model_name          = "gpt-4-basic"
  custom_llm_provider = "openai"
  base_model          = "gpt-4"
  model_api_key       = var.openai_api_key
}

# Create a basic team
resource "litellm_team" "basic" {
  team_alias = "my-team"
  models     = [litellm_model.basic.model_name]
}

# Create a basic API key
resource "litellm_key" "basic" {
  key_alias = "my-api-key"
  team_id   = litellm_team.basic.team_id
}

# Variables
variable "openai_api_key" {
  description = "OpenAI API key"
  type        = string
  sensitive   = true
}

# Outputs
output "api_key" {
  value     = litellm_key.basic.key
  sensitive = true
}

output "team_id" {
  value = litellm_team.basic.team_id
}
