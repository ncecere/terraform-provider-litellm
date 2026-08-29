# litellm_model - Access-group acceptance dependency
# The access-group API writes the reverse relation onto this model. Ignore that
# server-managed edge in this paired fixture so both resource lifecycles can
# converge without creating the group early through the model endpoint.

resource "litellm_model" "access_group" {
  model_name          = "test-model-access-group"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  mode                = "chat"
  additional_litellm_params = {
    allow_client_keepalive_override = "false"
    use_in_pass_through             = "false"
    use_litellm_proxy               = "false"
    use_xai_oauth                   = "false"
  }

  lifecycle {
    ignore_changes = [access_groups]
  }
}

output "model_access_group_id" {
  value = litellm_model.access_group.id
}
