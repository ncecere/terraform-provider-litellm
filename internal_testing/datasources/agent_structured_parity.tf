data "litellm_agent" "structured_advanced" {
  id = litellm_agent.structured_advanced.id
}

output "ds_agent_structured_projection" {
  value = {
    card        = data.litellm_agent.structured_advanced.agent_card_params_json
    params      = data.litellm_agent.structured_advanced.litellm_params_json
    permissions = data.litellm_agent.structured_advanced.object_permission_json
  }
  sensitive = true
}

locals {
  agent_structured_list_projection = one([
    for agent in data.litellm_agents.all.agents : {
      card        = agent.agent_card_params_json
      params      = agent.litellm_params_json
      permissions = agent.object_permission_json
    } if agent.agent_id == litellm_agent.structured_advanced.id
  ])
  agent_structured_single_projection = {
    card        = data.litellm_agent.structured_advanced.agent_card_params_json
    params      = data.litellm_agent.structured_advanced.litellm_params_json
    permissions = data.litellm_agent.structured_advanced.object_permission_json
  }
}

output "ds_agents_structured_parity" {
  value     = local.agent_structured_list_projection
  sensitive = true
}
