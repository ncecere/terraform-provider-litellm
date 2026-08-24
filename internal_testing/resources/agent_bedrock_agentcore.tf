# Bedrock AgentCore is represented by LiteLLM v1.98.0 as ordinary agent CRUD
# with provider/model routing parameters; there is no persisted agent_type.
# Registry lifecycle does not invoke AWS, so this placeholder ARN is safe for
# pinned local CRUD/no-drift/destroy validation.
resource "litellm_agent" "bedrock_agentcore" {
  agent_name = "smoke-bedrock-agentcore"

  agent_card {
    name             = "Smoke Bedrock AgentCore"
    url              = ""
    protocol_version = "1.0"

    capabilities {
      streaming = true
    }
  }

  litellm_params = {
    custom_llm_provider = "bedrock"
    model               = "bedrock/agentcore/arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/smoke"
    qualifier           = "PROD"
  }
}
