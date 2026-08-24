# data.litellm_agents - Lists all agents

data "litellm_agents" "all" {
  depends_on = [litellm_agent.minimal]
}

output "ds_agents_list" {
  value = data.litellm_agents.all
}
