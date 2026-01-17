# Multi-Provider LiteLLM Configuration
# This example demonstrates configuring multiple LLM providers

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {
  api_base = var.litellm_api_base
  api_key  = var.litellm_api_key
}

# =============================================================================
# CREDENTIALS FOR EACH PROVIDER
# =============================================================================

resource "litellm_credential" "openai" {
  credential_name = "openai-creds"
  credential_values = {
    "api_key" = var.openai_api_key
  }
}

resource "litellm_credential" "anthropic" {
  credential_name = "anthropic-creds"
  credential_values = {
    "api_key" = var.anthropic_api_key
  }
}

resource "litellm_credential" "azure" {
  credential_name = "azure-openai-creds"
  credential_values = {
    "api_key" = var.azure_openai_key
  }
}

resource "litellm_credential" "bedrock" {
  credential_name = "aws-bedrock-creds"
  credential_values = {
    "aws_access_key_id"     = var.aws_access_key_id
    "aws_secret_access_key" = var.aws_secret_access_key
  }
}

# =============================================================================
# OPENAI MODELS
# =============================================================================

resource "litellm_model" "openai_gpt4" {
  model_name              = "openai-gpt-4"
  custom_llm_provider     = "openai"
  base_model              = "gpt-4"
  litellm_credential_name = litellm_credential.openai.credential_name
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}

resource "litellm_model" "openai_gpt4_turbo" {
  model_name              = "openai-gpt-4-turbo"
  custom_llm_provider     = "openai"
  base_model              = "gpt-4-turbo-preview"
  litellm_credential_name = litellm_credential.openai.credential_name
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 10.0
  output_cost_per_million_tokens = 30.0
}

resource "litellm_model" "openai_embedding" {
  model_name              = "openai-embedding"
  custom_llm_provider     = "openai"
  base_model              = "text-embedding-3-small"
  litellm_credential_name = litellm_credential.openai.credential_name
  tier                    = "paid"
  mode                    = "embedding"

  input_cost_per_million_tokens = 0.02
}

# =============================================================================
# ANTHROPIC MODELS
# =============================================================================

resource "litellm_model" "anthropic_claude3_opus" {
  model_name              = "anthropic-claude-3-opus"
  custom_llm_provider     = "anthropic"
  base_model              = "claude-3-opus-20240229"
  litellm_credential_name = litellm_credential.anthropic.credential_name
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 15.0
  output_cost_per_million_tokens = 75.0
}

resource "litellm_model" "anthropic_claude3_sonnet" {
  model_name              = "anthropic-claude-3-sonnet"
  custom_llm_provider     = "anthropic"
  base_model              = "claude-3-sonnet-20240229"
  litellm_credential_name = litellm_credential.anthropic.credential_name
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 3.0
  output_cost_per_million_tokens = 15.0
}

resource "litellm_model" "anthropic_claude3_haiku" {
  model_name              = "anthropic-claude-3-haiku"
  custom_llm_provider     = "anthropic"
  base_model              = "claude-3-haiku-20240307"
  litellm_credential_name = litellm_credential.anthropic.credential_name
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 0.25
  output_cost_per_million_tokens = 1.25
}

# =============================================================================
# AZURE OPENAI MODELS
# =============================================================================

resource "litellm_model" "azure_gpt4" {
  model_name              = "azure-gpt-4"
  custom_llm_provider     = "azure"
  base_model              = "gpt-4"
  litellm_credential_name = litellm_credential.azure.credential_name
  model_api_base          = var.azure_openai_endpoint
  api_version             = "2024-02-15-preview"
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}

# =============================================================================
# AWS BEDROCK MODELS
# =============================================================================

resource "litellm_model" "bedrock_claude" {
  model_name              = "bedrock-claude"
  custom_llm_provider     = "bedrock"
  base_model              = "anthropic.claude-3-sonnet-20240229-v1:0"
  litellm_credential_name = litellm_credential.bedrock.credential_name
  aws_region_name         = var.aws_region
  tier                    = "paid"
  mode                    = "chat"

  input_cost_per_million_tokens  = 3.0
  output_cost_per_million_tokens = 15.0
}

resource "litellm_model" "bedrock_titan" {
  model_name              = "bedrock-titan-embed"
  custom_llm_provider     = "bedrock"
  base_model              = "amazon.titan-embed-text-v1"
  litellm_credential_name = litellm_credential.bedrock.credential_name
  aws_region_name         = var.aws_region
  tier                    = "paid"
  mode                    = "embedding"

  input_cost_per_million_tokens = 0.1
}

# =============================================================================
# ACCESS GROUPS BY PROVIDER
# =============================================================================

resource "litellm_access_group" "openai_models" {
  access_group_id   = "openai-all"
  access_group_name = "All OpenAI Models"
  members = [
    litellm_model.openai_gpt4.model_name,
    litellm_model.openai_gpt4_turbo.model_name,
    litellm_model.openai_embedding.model_name
  ]
}

resource "litellm_access_group" "anthropic_models" {
  access_group_id   = "anthropic-all"
  access_group_name = "All Anthropic Models"
  members = [
    litellm_model.anthropic_claude3_opus.model_name,
    litellm_model.anthropic_claude3_sonnet.model_name,
    litellm_model.anthropic_claude3_haiku.model_name
  ]
}

resource "litellm_access_group" "chat_models" {
  access_group_id   = "all-chat"
  access_group_name = "All Chat Models"
  members = [
    litellm_model.openai_gpt4.model_name,
    litellm_model.openai_gpt4_turbo.model_name,
    litellm_model.anthropic_claude3_opus.model_name,
    litellm_model.anthropic_claude3_sonnet.model_name,
    litellm_model.anthropic_claude3_haiku.model_name,
    litellm_model.azure_gpt4.model_name,
    litellm_model.bedrock_claude.model_name
  ]
}

resource "litellm_access_group" "embedding_models" {
  access_group_id   = "all-embedding"
  access_group_name = "All Embedding Models"
  members = [
    litellm_model.openai_embedding.model_name,
    litellm_model.bedrock_titan.model_name
  ]
}

# =============================================================================
# TEAMS WITH DIFFERENT ACCESS LEVELS
# =============================================================================

resource "litellm_team" "premium_access" {
  team_alias = "premium-users"
  models = concat(
    litellm_access_group.openai_models.members,
    litellm_access_group.anthropic_models.members,
    [litellm_model.azure_gpt4.model_name, litellm_model.bedrock_claude.model_name]
  )
  max_budget = 5000.0
}

resource "litellm_team" "standard_access" {
  team_alias = "standard-users"
  models = [
    litellm_model.openai_gpt4_turbo.model_name,
    litellm_model.anthropic_claude3_sonnet.model_name,
    litellm_model.anthropic_claude3_haiku.model_name
  ]
  max_budget = 1000.0
}

# =============================================================================
# OUTPUTS
# =============================================================================

output "all_model_names" {
  value = {
    openai = {
      gpt4       = litellm_model.openai_gpt4.model_name
      gpt4_turbo = litellm_model.openai_gpt4_turbo.model_name
      embedding  = litellm_model.openai_embedding.model_name
    }
    anthropic = {
      opus   = litellm_model.anthropic_claude3_opus.model_name
      sonnet = litellm_model.anthropic_claude3_sonnet.model_name
      haiku  = litellm_model.anthropic_claude3_haiku.model_name
    }
    azure = {
      gpt4 = litellm_model.azure_gpt4.model_name
    }
    bedrock = {
      claude = litellm_model.bedrock_claude.model_name
      titan  = litellm_model.bedrock_titan.model_name
    }
  }
}

output "team_ids" {
  value = {
    premium  = litellm_team.premium_access.team_id
    standard = litellm_team.standard_access.team_id
  }
}
