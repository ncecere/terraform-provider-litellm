# litellm_search_tool Data Source

Retrieves information about a specific LiteLLM search tool configuration.

## Example Usage

### Minimal Example

```hcl
data "litellm_search_tool" "existing" {
  search_tool_id = "search-tool-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_search_tool" "tavily" {
  search_tool_id = var.search_tool_id
}

output "search_tool_info" {
  value = {
    name     = data.litellm_search_tool.tavily.search_tool_name
    provider = data.litellm_search_tool.tavily.search_provider
    timeout  = data.litellm_search_tool.tavily.timeout
  }
}
```

## Argument Reference

The following arguments are supported:

* `search_tool_id` - (Required) The unique identifier of the search tool to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the search tool.
* `search_tool_id` - The search tool ID.
* `search_tool_name` - The search tool name.
* `search_provider` - The search provider (tavily, serper, bing, google).
* `api_base` - Base URL for the search API.
* `timeout` - Timeout in seconds for search requests.
* `max_retries` - Maximum number of retries.
* `search_tool_info` - JSON string of additional configuration.

## Notes

- API keys are not exposed in data source output for security reasons
