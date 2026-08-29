# LiteLLM v1.98.0 key-level router settings, including heterogeneous JSON
# fields and exact PascalCase nested retry-policy wire keys.
resource "litellm_key" "router_settings" {
  key_alias = "smoke-key-router-settings"
  models    = ["gpt-4o", "gpt-4o-mini"]

  router_settings = {
    routing_strategy      = "simple-shuffle"
    routing_strategy_args = jsonencode({ ttl = 30, nested = { weight = 0.5 } })
    routing_groups = jsonencode([{
      group_name            = "primary"
      models                = ["gpt-4o"]
      routing_strategy      = "simple-shuffle"
      routing_strategy_args = { weight = 2 }
    }])
    model_group_retry_policy    = jsonencode({ "gpt-4o" = { RateLimitErrorRetries = 7 } })
    model_group_affinity_config = jsonencode({ us = ["gpt-4o", "gpt-4o-mini"] })
    allowed_fails               = 8
    cooldown_time               = 9.5
    num_retries                 = 10
    timeout                     = 11.5
    max_retries                 = 12
    retry_after                 = 0.25
    fallbacks                   = jsonencode([{ "gpt-4o" = ["gpt-4o-mini"] }, { "*" = ["emergency-model"] }])
    context_window_fallbacks    = jsonencode([{ "gpt-4o" = ["large-context"] }])
    model_group_alias           = jsonencode({ fast = "gpt-4o-mini", hidden = { model = "gpt-4o", hidden = true } })
    enable_tag_filtering        = true
    tag_routing_prefix          = "tenant:"

    retry_policy = {
      bad_request_error_retries              = 1
      authentication_error_retries           = 2
      timeout_error_retries                  = 3
      rate_limit_error_retries               = 4
      content_policy_violation_error_retries = 5
      internal_server_error_retries          = 6
    }
  }
}
