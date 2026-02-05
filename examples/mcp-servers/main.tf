# MCP Servers Example
# This example demonstrates configuring various MCP server types

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {}

# =============================================================================
# TEAMS FOR ACCESS CONTROL
# =============================================================================

resource "litellm_team" "developers" {
  team_alias = "developers"
}

resource "litellm_team" "automation" {
  team_alias = "automation-team"
}

# =============================================================================
# HTTP MCP SERVERS
# =============================================================================

# Minimal HTTP server
resource "litellm_mcp_server" "simple_http" {
  server_name = "simple-api"
  url         = "https://api.example.com/mcp"
  transport   = "http"
}

# Full HTTP server with authentication
resource "litellm_mcp_server" "github" {
  server_name  = "github-integration"
  alias        = "github"
  description  = "GitHub API integration for repository operations"
  url          = "https://api.github.com/mcp"
  transport    = "http"
  auth_type    = "bearer"
  spec_version = "2024-11-05"

  credentials = {
    "token" = var.github_token
  }

  allowed_tools = [
    "list_repositories",
    "read_file",
    "search_code",
    "create_issue"
  ]

  extra_headers = {
    "X-GitHub-Api-Version" = "2022-11-28"
  }

  mcp_access_groups = [litellm_team.developers.team_id]
  allow_all_keys    = false

  mcp_info {
    server_name = "GitHub Integration"
    description = "Access GitHub repositories and code"
    logo_url    = "https://github.githubassets.com/images/modules/logos_page/GitHub-Mark.png"

    mcp_server_cost_info {
      default_cost_per_query = 0.01

      tool_name_to_cost_per_query = {
        "search_code"  = 0.05
        "create_issue" = 0.02
      }
    }
  }
}

# =============================================================================
# SSE MCP SERVERS
# =============================================================================

# Zapier integration with SSE
resource "litellm_mcp_server" "zapier" {
  server_name = "zapier-automation"
  alias       = "zapier"
  description = "Zapier workflow automation"
  url         = "https://actions.zapier.com/mcp/sse"
  transport   = "sse"
  auth_type   = "bearer"

  credentials = {
    "api_key" = var.zapier_api_key
  }

  mcp_access_groups = [litellm_team.automation.team_id]

  mcp_info {
    server_name = "Zapier Automation"
    description = "Trigger and manage Zapier workflows"

    mcp_server_cost_info {
      default_cost_per_query = 0.05

      tool_name_to_cost_per_query = {
        "send_email"      = 0.10
        "create_document" = 0.08
        "update_database" = 0.03
      }
    }
  }
}

# =============================================================================
# OAUTH MCP SERVERS
# =============================================================================

resource "litellm_mcp_server" "oauth_protected" {
  server_name = "enterprise-api"
  alias       = "enterprise"
  description = "Enterprise API with OAuth authentication"
  url         = "https://api.enterprise.com/mcp"
  transport   = "http"
  auth_type   = "bearer"

  authorization_url = "https://auth.enterprise.com/oauth/authorize"
  token_url         = "https://auth.enterprise.com/oauth/token"
  registration_url  = "https://auth.enterprise.com/oauth/register"

  credentials = {
    "client_id"     = var.oauth_client_id
    "client_secret" = var.oauth_client_secret
  }

  static_headers = {
    "Accept"       = "application/json"
    "Content-Type" = "application/json"
  }

  allow_all_keys = false
  mcp_access_groups = [
    litellm_team.developers.team_id,
    litellm_team.automation.team_id
  ]
}

# =============================================================================
# STDIO MCP SERVERS (Local)
# =============================================================================

resource "litellm_mcp_server" "local_python" {
  server_name = "local-python-tools"
  alias       = "python-tools"
  description = "Local Python development tools"
  url         = "stdio://python-tools"
  transport   = "stdio"
  auth_type   = "none"

  command = "python3"
  args    = ["-m", "mcp_server", "--config", "/etc/mcp/config.yaml"]

  env = {
    "PYTHONPATH" = "/opt/mcp-servers"
    "DEBUG"      = "false"
    "LOG_LEVEL"  = "info"
  }

  mcp_info {
    server_name = "Python Development Tools"
    description = "Local tools for Python development"

    mcp_server_cost_info {
      default_cost_per_query = 0.0 # Free for local
    }
  }
}

resource "litellm_mcp_server" "local_nodejs" {
  server_name = "local-nodejs-tools"
  alias       = "nodejs-tools"
  description = "Local Node.js development tools"
  url         = "stdio://nodejs-tools"
  transport   = "stdio"
  auth_type   = "none"

  command = "node"
  args    = ["/opt/mcp-servers/nodejs/server.js"]

  env = {
    "NODE_ENV"  = "development"
    "LOG_LEVEL" = "debug"
  }
}

# =============================================================================
# VARIABLES
# =============================================================================

variable "github_token" {
  description = "GitHub personal access token"
  type        = string
  sensitive   = true
}

variable "zapier_api_key" {
  description = "Zapier API key"
  type        = string
  sensitive   = true
}

variable "oauth_client_id" {
  description = "OAuth client ID"
  type        = string
}

variable "oauth_client_secret" {
  description = "OAuth client secret"
  type        = string
  sensitive   = true
}

# =============================================================================
# OUTPUTS
# =============================================================================

output "mcp_server_ids" {
  value = {
    github       = litellm_mcp_server.github.server_id
    zapier       = litellm_mcp_server.zapier.server_id
    enterprise   = litellm_mcp_server.oauth_protected.server_id
    python_local = litellm_mcp_server.local_python.server_id
    nodejs_local = litellm_mcp_server.local_nodejs.server_id
  }
}
