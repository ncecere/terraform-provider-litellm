# litellm_search_tools Data Source

Retrieves a list of all LiteLLM search tool configurations.

## Example Usage

### Minimal Example

```hcl
data "litellm_search_tools" "all" {}
```

### Full Example

```hcl
data "litellm_search_tools" "all" {}

output "search_tool_count" {
  value = length(data.litellm_search_tools.all.search_tools)
}

output "search_tool_names" {
  value = [for s in data.litellm_search_tools.all.search_tools : s.search_tool_name]
}

# Find Tavily search tools
locals {
  tavily_tools = [
    for s in data.litellm_search_tools.all.search_tools : s
    if s.search_provider == "tavily"
  ]
}

output "tavily_tool_count" {
  value = length(local.tavily_tools)
}

# Group by provider
locals {
  tools_by_provider = {
    for s in data.litellm_search_tools.all.search_tools :
    s.search_provider => s.search_tool_name...
  }
}

output "tools_by_provider" {
  value = local.tools_by_provider
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `search_tools` - List of search tool objects, each containing:
  * `search_tool_id` - The unique identifier.
  * `search_tool_name` - The search tool name.
  * `search_provider` - The search provider.
  * `api_base` - Base URL for the search API.
  * `timeout` - Timeout in seconds.
  * `max_retries` - Maximum retries.
