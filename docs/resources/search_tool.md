# litellm_search_tool (Resource)

Manages a search tool configuration in LiteLLM. Search tools allow LLM models to perform web searches using providers such as Tavily, Serper, Bing, or Google.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_search_tool" "minimal" {
  search_tool_name = "my-search"
  search_provider  = "tavily"
}
```

### Full Configuration

```hcl
resource "litellm_search_tool" "full" {
  search_tool_name = "tavily-search"
  search_provider  = "tavily"
  api_key          = "tvly-your-api-key"
  api_base         = "https://api.tavily.com"
  timeout          = 30.0
  max_retries      = 3

  search_tool_info = jsonencode({
    "description" = "Web search tool"
    "category"    = "web-search"
  })
}
```

## Argument Reference

The following arguments are supported:

- `search_tool_name` - (Required) The name of the search tool.
- `search_provider` - (Required) The search provider to use. Supported values include `tavily`, `serper`, `bing`, and `google`.
- `api_key` - (Optional, Sensitive) The API key for authenticating with the search provider.
- `api_base` - (Optional) The base URL for the search provider API.
- `timeout` - (Optional) Request timeout in seconds for search requests.
- `max_retries` - (Optional) Maximum number of retry attempts for failed search requests.
- `search_tool_info` - (Optional) A JSON string containing additional search tool configuration. Use `jsonencode()` to construct this value. The provider automatically parses it into a JSON object when sending to the API.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

- `id` - The internal resource identifier.
- `search_tool_id` - The unique identifier assigned to the search tool by LiteLLM.

## Import

Search tools can be imported using the search tool ID:

```shell
terraform import litellm_search_tool.example <search-tool-id>
```
