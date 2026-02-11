# litellm_search_tool - Minimal
# Only required attributes

resource "litellm_search_tool" "minimal" {
  search_tool_name = "test-search-minimal"
  search_provider  = "tavily"
}

output "search_tool_minimal_id" {
  value = litellm_search_tool.minimal.id
}
