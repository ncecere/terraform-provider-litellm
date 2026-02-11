# litellm_search_tool - Full
# All attributes populated

resource "litellm_search_tool" "full" {
  search_tool_name = "test-search-full"
  search_provider  = "tavily"
  api_key          = "tvly-fake-api-key"
  api_base         = "https://api.tavily.com"
  timeout          = 30.0
  max_retries      = 3

  search_tool_info = jsonencode({
    "description" = "Full test search tool"
    "category"    = "web-search"
  })
}

output "search_tool_full_id" {
  value = litellm_search_tool.full.id
}
