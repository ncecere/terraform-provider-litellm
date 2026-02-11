# Data Sources Example
# This example demonstrates using data sources to reference existing resources

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {}

# =============================================================================
# FETCH EXISTING RESOURCES
# =============================================================================

# Get all models
data "litellm_models" "all" {}

# Get all teams
data "litellm_teams" "all" {}

# Get all users
data "litellm_users" "all" {}

# Get specific resources
data "litellm_team" "engineering" {
  team_id = var.engineering_team_id
}

data "litellm_organization" "main" {
  organization_id = var.organization_id
}

# =============================================================================
# ANALYZE MODEL INVENTORY
# =============================================================================

locals {
  # Group models by provider
  models_by_provider = {
    for m in data.litellm_models.all.models :
    m.custom_llm_provider => m.model_name...
  }

  # Find paid tier models
  paid_models = [
    for m in data.litellm_models.all.models : m.model_name
    if m.tier == "paid"
  ]

  # Find chat models
  chat_models = [
    for m in data.litellm_models.all.models : m.model_name
    if m.mode == "chat"
  ]
}

output "model_inventory" {
  value = {
    total_models     = length(data.litellm_models.all.models)
    by_provider      = local.models_by_provider
    paid_model_count = length(local.paid_models)
    chat_model_count = length(local.chat_models)
  }
}

# =============================================================================
# ANALYZE TEAM SPEND
# =============================================================================

locals {
  total_team_spend = sum([for t in data.litellm_teams.all.teams : t.spend])
  total_budget     = sum([for t in data.litellm_teams.all.teams : t.max_budget])

  budget_utilization = local.total_budget > 0 ? (local.total_team_spend / local.total_budget) * 100 : 0

  teams_over_80_percent = [
    for t in data.litellm_teams.all.teams : t.team_alias
    if t.max_budget > 0 && (t.spend / t.max_budget) > 0.8
  ]
}

output "spend_analysis" {
  value = {
    total_spend         = local.total_team_spend
    total_budget        = local.total_budget
    utilization_percent = local.budget_utilization
    teams_near_limit    = local.teams_over_80_percent
  }
}

# =============================================================================
# ANALYZE USER DISTRIBUTION
# =============================================================================

locals {
  admin_count = length([
    for u in data.litellm_users.all.users : u
    if u.user_role == "proxy_admin"
  ])

  user_count = length([
    for u in data.litellm_users.all.users : u
    if u.user_role == "internal_user"
  ])
}

output "user_analysis" {
  value = {
    total_users = length(data.litellm_users.all.users)
    admin_count = local.admin_count
    user_count  = local.user_count
  }
}

# =============================================================================
# CREATE RESOURCES BASED ON EXISTING DATA
# =============================================================================

# Create a new team with access to all paid models
resource "litellm_team" "paid_model_users" {
  team_alias      = "paid-model-access"
  organization_id = data.litellm_organization.main.organization_id
  models          = local.paid_models
  max_budget      = 1000.0
}

# Create API key for engineering team
resource "litellm_key" "engineering_key" {
  key_alias  = "engineering-data-key"
  team_id    = data.litellm_team.engineering.team_id
  max_budget = data.litellm_team.engineering.max_budget * 0.5
}

# =============================================================================
# VARIABLES
# =============================================================================

variable "engineering_team_id" {
  description = "ID of the engineering team"
  type        = string
}

variable "organization_id" {
  description = "ID of the organization"
  type        = string
}
