# Complete LiteLLM Provider Configuration
# This example demonstrates a full enterprise setup with all resource types

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {
  api_base             = var.litellm_api_base
  api_key              = var.litellm_api_key
  insecure_skip_verify = var.insecure_skip_verify
  litellm_changed_by   = "terraform-enterprise"
}

# =============================================================================
# CREDENTIALS
# =============================================================================

resource "litellm_credential" "openai" {
  credential_name = "openai-production"
  credential_values = {
    "api_key" = var.openai_api_key
  }
  credential_info = {
    "environment" = "production"
    "provider"    = "openai"
  }
}

resource "litellm_credential" "anthropic" {
  credential_name = "anthropic-production"
  credential_values = {
    "api_key" = var.anthropic_api_key
  }
}

# =============================================================================
# MODELS
# =============================================================================

resource "litellm_model" "gpt4" {
  model_name              = "gpt-4-enterprise"
  custom_llm_provider     = "openai"
  base_model              = "gpt-4"
  tier                    = "paid"
  mode                    = "chat"
  litellm_credential_name = litellm_credential.openai.credential_name
  tpm                     = 100000
  rpm                     = 1000

  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}

resource "litellm_model" "gpt35" {
  model_name              = "gpt-35-enterprise"
  custom_llm_provider     = "openai"
  base_model              = "gpt-3.5-turbo"
  tier                    = "paid"
  mode                    = "chat"
  litellm_credential_name = litellm_credential.openai.credential_name

  input_cost_per_million_tokens  = 0.5
  output_cost_per_million_tokens = 1.5
}

resource "litellm_model" "claude" {
  model_name              = "claude-3-enterprise"
  custom_llm_provider     = "anthropic"
  base_model              = "claude-3-sonnet-20240229"
  tier                    = "paid"
  mode                    = "chat"
  litellm_credential_name = litellm_credential.anthropic.credential_name

  input_cost_per_million_tokens  = 3.0
  output_cost_per_million_tokens = 15.0
}

resource "litellm_model" "embedding" {
  model_name              = "embedding-enterprise"
  custom_llm_provider     = "openai"
  base_model              = "text-embedding-3-small"
  tier                    = "paid"
  mode                    = "embedding"
  litellm_credential_name = litellm_credential.openai.credential_name

  input_cost_per_million_tokens = 0.02
}

# =============================================================================
# ORGANIZATION & TEAMS
# =============================================================================

resource "litellm_organization" "enterprise" {
  organization_alias = "enterprise-org"
  max_budget         = 10000.0
  budget_duration    = "monthly"
  models = [
    litellm_model.gpt4.model_name,
    litellm_model.gpt35.model_name,
    litellm_model.claude.model_name,
    litellm_model.embedding.model_name
  ]
}

resource "litellm_team" "engineering" {
  team_alias      = "engineering"
  organization_id = litellm_organization.enterprise.organization_id
  max_budget      = 3000.0
  budget_duration = "monthly"
  models = [
    litellm_model.gpt4.model_name,
    litellm_model.gpt35.model_name,
    litellm_model.claude.model_name
  ]
  tpm_limit = 200000
  rpm_limit = 2000
}

resource "litellm_team" "data_science" {
  team_alias      = "data-science"
  organization_id = litellm_organization.enterprise.organization_id
  max_budget      = 2000.0
  budget_duration = "monthly"
  models = [
    litellm_model.gpt4.model_name,
    litellm_model.embedding.model_name
  ]
}

resource "litellm_team" "support" {
  team_alias      = "customer-support"
  organization_id = litellm_organization.enterprise.organization_id
  max_budget      = 500.0
  budget_duration = "monthly"
  models = [
    litellm_model.gpt35.model_name
  ]
}

# =============================================================================
# USERS
# =============================================================================

resource "litellm_user" "admin" {
  user_email = "admin@enterprise.com"
  user_alias = "system-admin"
  user_role  = "proxy_admin"
}

resource "litellm_user" "developer" {
  user_email = "developer@enterprise.com"
  user_alias = "lead-developer"
  user_role  = "internal_user"
  max_budget = 500.0
  models     = [litellm_model.gpt4.model_name, litellm_model.gpt35.model_name]
}

# =============================================================================
# TEAM MEMBERSHIPS
# =============================================================================

resource "litellm_team_member" "admin_engineering" {
  team_id = litellm_team.engineering.team_id
  user_id = litellm_user.admin.user_id
  role    = "admin"
}

resource "litellm_team_member" "developer_engineering" {
  team_id = litellm_team.engineering.team_id
  user_id = litellm_user.developer.user_id
  role    = "user"
}

# =============================================================================
# API KEYS
# =============================================================================

resource "litellm_key" "engineering_api" {
  key_alias       = "engineering-production"
  team_id         = litellm_team.engineering.team_id
  max_budget      = 1000.0
  budget_duration = "monthly"
  tpm_limit       = 100000
  rpm_limit       = 1000
  tags            = ["production", "engineering"]
}

resource "litellm_key" "support_api" {
  key_alias       = "support-api-key"
  team_id         = litellm_team.support.team_id
  max_budget      = 200.0
  budget_duration = "monthly"
  tags            = ["production", "support"]
}

# =============================================================================
# ACCESS GROUPS
# =============================================================================

resource "litellm_access_group" "premium" {
  access_group = "premium-models"
  model_names = [
    litellm_model.gpt4.model_name,
    litellm_model.claude.model_name
  ]
}

resource "litellm_access_group" "standard" {
  access_group = "standard-models"
  model_names = [
    litellm_model.gpt35.model_name
  ]
}

# =============================================================================
# TAGS
# =============================================================================

resource "litellm_tag" "production" {
  name = "production"
}

resource "litellm_tag" "cost_center_rd" {
  name = "cost-center-rd"
}

# =============================================================================
# PROMPTS
# =============================================================================

resource "litellm_prompt" "support_agent" {
  prompt_id   = "customer-support-agent"
  prompt_type = "db"

  dotprompt_content = <<-EOT
    You are a helpful customer support agent for Enterprise Corp.
    
    Guidelines:
    - Be polite, professional, and empathetic
    - Provide accurate information about our products and services
    - Escalate complex issues to human support when needed
    - Never share sensitive customer data or internal policies
    
    Always aim to resolve the customer's issue in the first interaction.
  EOT
}

resource "litellm_prompt" "code_assistant" {
  prompt_id   = "code-assistant"
  prompt_type = "db"

  dotprompt_content = <<-EOT
    You are an expert software developer assistant.
    
    When answering questions:
    1. Provide clear, well-documented code examples
    2. Explain your reasoning and approach
    3. Consider edge cases and error handling
    4. Follow best practices and coding standards
    5. Suggest improvements when appropriate
  EOT
}

# =============================================================================
# GUARDRAILS
# =============================================================================

resource "litellm_guardrail" "content_safety" {
  guardrail_name = "enterprise-content-safety"
  guardrail      = "custom"
  mode           = "post_call"

  litellm_params = jsonencode({
    check_pii        = true
    check_toxicity   = true
    redact_sensitive = true

    blocked_topics = [
      "competitor_information",
      "financial_advice",
      "medical_advice"
    ]
  })
}

# =============================================================================
# MCP SERVERS
# =============================================================================

resource "litellm_mcp_server" "github" {
  server_name = "github-integration"
  alias       = "github"
  description = "GitHub MCP server for repository operations"
  url         = "https://api.github.com/mcp"
  transport   = "http"
  auth_type   = "bearer"

  credentials = {
    "token" = var.github_token
  }

  allowed_tools = ["read_file", "list_repos", "search_code"]

  mcp_access_groups = [
    litellm_team.engineering.team_id
  ]
}

# =============================================================================
# SEARCH TOOLS
# =============================================================================

resource "litellm_search_tool" "tavily" {
  search_tool_name = "web-search"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
  timeout          = 30.0
  max_retries      = 3

  search_tool_info = jsonencode({
    search_depth = "advanced"
    max_results  = 10
  })
}

# =============================================================================
# VECTOR STORES
# =============================================================================

resource "litellm_vector_store" "knowledge_base" {
  vector_store_name        = "enterprise-knowledge-base"
  custom_llm_provider      = "pinecone"
  litellm_credential_name  = "pinecone-credential"
  vector_store_description = "Enterprise knowledge base for RAG"

  vector_store_metadata = {
    "environment" = "production"
    "department"  = "engineering"
  }
}

# =============================================================================
# OUTPUTS
# =============================================================================

output "organization_id" {
  value = litellm_organization.enterprise.organization_id
}

output "team_ids" {
  value = {
    engineering  = litellm_team.engineering.team_id
    data_science = litellm_team.data_science.team_id
    support      = litellm_team.support.team_id
  }
}

output "api_keys" {
  value = {
    engineering = litellm_key.engineering_api.key
    support     = litellm_key.support_api.key
  }
  sensitive = true
}

output "model_names" {
  value = {
    gpt4      = litellm_model.gpt4.model_name
    gpt35     = litellm_model.gpt35.model_name
    claude    = litellm_model.claude.model_name
    embedding = litellm_model.embedding.model_name
  }
}
