# data.litellm_agents - Lists all agents

data "litellm_agents" "all" {
}

output "ds_agents_list" {
  value = data.litellm_agents.all
}
