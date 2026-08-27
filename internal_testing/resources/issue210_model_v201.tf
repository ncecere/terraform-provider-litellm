# Upgrade fixture that explicitly owns v2.0.1 computed defaults so the current
# provider does not interpret their absence as an intentional clear.
resource "litellm_model" "upgrade" {
  model_name          = "issue210-model-upgrade"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  mode                = "chat"
  access_groups       = []
  additional_litellm_params = {
    allow_client_keepalive_override = "false"
    use_in_pass_through             = "false"
    use_litellm_proxy               = "false"
    use_xai_oauth                   = "false"
  }
}
