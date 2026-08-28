terraform {
  required_version = ">= 1.1.0"

  required_providers {
    litellm = {
      source  = "registry.terraform.io/ncecere/litellm"
      version = ">= 2.0.1, < 3.0.0"
    }
  }
}

provider "litellm" {}

resource "litellm_mcp_toolset" "incident_response" {
  toolset_name = "incident-response"
  description  = "Read-only incident response tools"

  tools = [
    {
      server_id = "pagerduty-server-id"
      tool_name = "list_incidents"
    },
    {
      server_id = "datadog-server-id"
      tool_name = "search_datadog_logs"
    }
  ]
}

resource "litellm_mcp_toolset" "empty" {
  toolset_name = "empty"
}

resource "litellm_team" "responders" {
  team_alias      = "Responders"
  mcp_toolset_ids = [litellm_mcp_toolset.incident_response.toolset_id]
}

resource "litellm_key" "responder" {
  key_alias       = "incident-response"
  team_id         = litellm_team.responders.id
  mcp_toolset_ids = [litellm_mcp_toolset.incident_response.toolset_id]
}
