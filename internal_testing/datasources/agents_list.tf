# data.litellm_agents - Lists all agents

data "litellm_agents" "all" {
  depends_on = [
    litellm_agent.minimal,
    litellm_agent.bedrock_agentcore,
    litellm_agent.mcp_tool_permissions,
    litellm_agent.structured_advanced,
  ]
}

output "ds_agents_list" {
  value     = data.litellm_agents.all
  sensitive = true
}
