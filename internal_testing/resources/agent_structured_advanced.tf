# Pinned-v1.98 disposable structured Agent lifecycle fixture. All names and
# targets are acceptance-only placeholders; this never imports or mutates a
# pre-existing development object.
resource "litellm_agent" "structured_advanced" {
  agent_name = "smoke-agent-structured-advanced"

  litellm_params = {
    model               = "bedrock/agentcore/arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/smoke-structured"
    custom_llm_provider = "bedrock"
    scalar_text         = "false"
    leading_text        = "001"
    json_looking_text   = "{\"still\":\"text\"}"
  }
  litellm_params_json = jsonencode({
    enabled      = false
    exact_large  = 9007199254740993
    explicit_nil = null
    empty_list   = []
    empty_object = {}
    nested = {
      routing = [
        { region = "us-east-1", weight = 1 },
        { region = "us-west-2", weight = 0.25 },
      ]
    }
  })

  agent_card {
    name             = "Smoke Structured Advanced"
    url              = ""
    version          = "1.0.0"
    protocol_version = "1.0"

    capabilities { streaming = true }

    provider {
      organization = "Acceptance"
      url          = "https://agent.example.invalid"
    }

    skills {
      id          = "structured"
      name        = "Structured"
      description = "Exercises ordered security requirements"
      tags        = ["acceptance", "structured"]
      security = [
        { oauth2 = ["read", "write", "write"] },
        { api_key = [] },
        { oauth2 = ["read", "write", "write"] },
      ]
    }

    skills {
      id            = "structured-null-security"
      name          = "Structured Null Security"
      security_json = jsonencode(null)
    }

    skills {
      id   = "structured-omitted-security"
      name = "Structured Omitted Security"
    }

    signatures {
      protected = "acceptance-protected"
      signature = "acceptance-signature"
      header = jsonencode({
        kid    = "acceptance-key"
        exact  = 9007199254740993
        nested = { empty = [] }
      })
    }
    signatures {
      protected   = "acceptance-protected-null"
      signature   = "acceptance-signature"
      header_json = jsonencode(null)
    }
    signatures {
      protected = "acceptance-protected-omitted"
      signature = "acceptance-signature"
    }
  }

  object_permission {
    mcp_servers = []
    mcp_tool_permissions = {
      "acceptance-server" = jsonencode(["tool-a", "tool-a"])
    }
  }
}

output "agent_structured_advanced_id" {
  value = litellm_agent.structured_advanced.id
}
