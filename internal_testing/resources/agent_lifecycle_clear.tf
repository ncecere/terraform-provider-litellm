variable "agent_lifecycle_phase" {
  description = "Acceptance-only lifecycle phase."
  type        = string
  default     = "set"

  validation {
    condition     = contains(["set", "cleared", "adversarial"], var.agent_lifecycle_phase)
    error_message = "agent_lifecycle_phase must be set, cleared, or adversarial."
  }
}

locals {
  agent_lifecycle_cleared     = var.agent_lifecycle_phase != "set"
  agent_lifecycle_adversarial = var.agent_lifecycle_phase == "adversarial"
}

resource "litellm_agent" "lifecycle" {
  agent_name        = "smoke-agent-lifecycle"
  tpm_limit         = local.agent_lifecycle_cleared ? null : 1200
  rpm_limit         = local.agent_lifecycle_cleared ? null : 120
  session_tpm_limit = local.agent_lifecycle_cleared ? null : 600
  session_rpm_limit = local.agent_lifecycle_cleared ? null : 60

  # v1.98 cannot clear the complete parameter object, but it can replace a
  # nonempty object and thereby remove individual keys.
  litellm_params = local.agent_lifecycle_cleared ? {
    model = local.agent_lifecycle_adversarial ? "openai/gpt-4o-mini-adversarial" : "openai/gpt-4o-mini"
    } : {
    model     = "openai/gpt-4o-mini"
    qualifier = "acceptance"
  }

  static_headers = local.agent_lifecycle_cleared ? {} : {
    "X-Agent-Acceptance" = "set"
  }
  extra_headers = local.agent_lifecycle_cleared ? [] : ["X-Agent-Acceptance"]

  agent_card {
    name                 = "Smoke Agent Lifecycle"
    description          = local.agent_lifecycle_adversarial ? "adversarial unrelated card update" : (local.agent_lifecycle_cleared ? null : "set then clear")
    url                  = "https://agent.example.com/lifecycle"
    version              = "1.0.0"
    protocol_version     = "1.0"
    default_input_modes  = ["text"]
    default_output_modes = ["text"]

    preferred_transport                  = local.agent_lifecycle_cleared ? null : "JSONRPC"
    icon_url                             = local.agent_lifecycle_cleared ? null : "https://agent.example.com/icon.png"
    documentation_url                    = local.agent_lifecycle_cleared ? null : "https://agent.example.com/docs"
    supports_authenticated_extended_card = local.agent_lifecycle_cleared ? false : true

    capabilities {
      streaming                = local.agent_lifecycle_cleared ? false : true
      push_notifications       = false
      state_transition_history = false
    }

    # Complete provider removal defaults to LiteLLM-owned metadata. Keep the
    # URL while clearing the independently supported organization field.
    provider {
      organization = local.agent_lifecycle_cleared ? null : "Acceptance"
      url          = "https://agent.example.com"
    }

    skills {
      id           = "acceptance"
      name         = "Acceptance"
      description  = local.agent_lifecycle_cleared ? null : "set then clear"
      tags         = local.agent_lifecycle_cleared ? [] : ["lifecycle"]
      examples     = local.agent_lifecycle_cleared ? [] : ["test"]
      input_modes  = local.agent_lifecycle_cleared ? [] : ["text"]
      output_modes = local.agent_lifecycle_cleared ? [] : ["text"]
      security = local.agent_lifecycle_cleared ? [] : [
        { oauth2 = ["read", "read"] },
      ]
    }

    dynamic "signatures" {
      for_each = local.agent_lifecycle_cleared ? [] : [1, 1]
      content {
        protected = "acceptance-protected"
        signature = "acceptance-signature"
        header    = jsonencode({ duplicate = signatures.key, exact = 9007199254740993 })
      }
    }
  }

  object_permission {
    mcp_servers       = local.agent_lifecycle_cleared ? [] : ["acceptance-server"]
    mcp_access_groups = local.agent_lifecycle_cleared ? [] : ["acceptance-group"]
    mcp_tool_permissions = local.agent_lifecycle_cleared ? {} : {
      "acceptance-server" = jsonencode(["acceptance-tool"])
    }
    models = local.agent_lifecycle_cleared ? [] : ["openai/gpt-4o-mini"]
    agents = local.agent_lifecycle_cleared ? [] : ["acceptance-agent"]
  }
}

output "agent_lifecycle_id" {
  value = litellm_agent.lifecycle.id
}
