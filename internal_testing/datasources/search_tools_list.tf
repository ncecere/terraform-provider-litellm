# data.litellm_search_tools - Lists all search tools

data "litellm_search_tools" "all" {
}

output "ds_search_tools_list" {
  value = data.litellm_search_tools.all
}
