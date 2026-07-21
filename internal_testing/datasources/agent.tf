# data.litellm_agent - Looks up an agent by id
# Note: references the agent created by resources/agent_minimal.tf

data "litellm_agent" "lookup" {
  id = litellm_agent.minimal.id
}

output "ds_agent_name" {
  value = data.litellm_agent.lookup.agent_name
}
