# data.litellm_search_tool - Looks up a search tool by search_tool_id
# Note: search_tool_id must reference an existing search tool

data "litellm_search_tool" "lookup" {
  search_tool_id = litellm_search_tool.minimal.search_tool_id
}

output "ds_search_tool_name" {
  value = data.litellm_search_tool.lookup.search_tool_name
}

output "ds_search_tool_provider" {
  value = data.litellm_search_tool.lookup.search_provider
}

output "ds_search_tool_timeout" {
  value = data.litellm_search_tool.lookup.timeout
}
