# Import fixture matching the canonical API projection returned without provider-private metadata.
resource "litellm_model" "upgrade" {
  model_name          = "issue210-model-upgrade"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  mode                = "chat"
  additional_litellm_params = {
    allow_client_keepalive_override    = "false"
    merge_reasoning_content_in_choices = "false"
    use_in_pass_through                = "false"
    use_litellm_proxy                  = "false"
    use_xai_oauth                      = "false"
  }
  additional_model_info = {}
}
