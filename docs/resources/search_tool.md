# litellm_search_tool Resource

Manages a LiteLLM search tool configuration. Search tools enable LLMs to perform web searches and retrieve information from external sources during conversations.

## Example Usage

### Minimal Example

```hcl
resource "litellm_search_tool" "basic" {
  search_tool_name = "web-search"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
}
```

### Full Example with Tavily

```hcl
resource "litellm_search_tool" "tavily_search" {
  search_tool_name = "tavily-web-search"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
  api_base         = "https://api.tavily.com"
  timeout          = 30.0
  max_retries      = 3
  
  search_tool_info = jsonencode({
    description  = "Web search for real-time information"
    search_depth = "advanced"
    max_results  = 10
  })
}
```

### Serper Search Tool

```hcl
resource "litellm_search_tool" "serper_search" {
  search_tool_name = "serper-google-search"
  search_provider  = "serper"
  api_key          = var.serper_api_key
  timeout          = 15.0
  max_retries      = 2
  
  search_tool_info = jsonencode({
    gl = "us"      # Google country code
    hl = "en"      # Language
    num = 10       # Number of results
  })
}
```

### Bing Search Tool

```hcl
resource "litellm_search_tool" "bing_search" {
  search_tool_name = "bing-web-search"
  search_provider  = "bing"
  api_key          = var.bing_api_key
  api_base         = "https://api.bing.microsoft.com/v7.0"
  timeout          = 20.0
  max_retries      = 3
  
  search_tool_info = jsonencode({
    market       = "en-US"
    safe_search  = "moderate"
    count        = 10
  })
}
```

### Google Custom Search Tool

```hcl
resource "litellm_search_tool" "google_search" {
  search_tool_name = "google-custom-search"
  search_provider  = "google"
  api_key          = var.google_api_key
  timeout          = 15.0
  max_retries      = 2
  
  search_tool_info = jsonencode({
    cx           = var.google_search_engine_id
    safe         = "active"
    num          = 10
    date_restrict = "m1"  # Last month
  })
}
```

### Multiple Search Tools

```hcl
# Primary search tool
resource "litellm_search_tool" "primary" {
  search_tool_name = "primary-search"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
  timeout          = 30.0
  max_retries      = 3
}

# Fallback search tool
resource "litellm_search_tool" "fallback" {
  search_tool_name = "fallback-search"
  search_provider  = "serper"
  api_key          = var.serper_api_key
  timeout          = 15.0
  max_retries      = 2
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `search_tool_name` - (Required) Unique name for the search tool.
* `search_provider` - (Required) The search provider to use. Valid values include: `tavily`, `serper`, `bing`, `google`.

### Optional Arguments

* `api_key` - (Optional, Sensitive) API key for the search provider.
* `api_base` - (Optional) Base URL for the search API. Uses provider default if not specified.
* `timeout` - (Optional) Timeout in seconds for search requests.
* `max_retries` - (Optional) Maximum number of retries for failed requests.
* `search_tool_info` - (Optional) JSON string containing additional provider-specific configuration.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this search tool.
* `search_tool_id` - The search tool ID (same as id).

## Import

Search tools can be imported using the search tool ID:

```shell
terraform import litellm_search_tool.example search-tool-xxxxxxxxxxxx
```

## Supported Search Providers

### Tavily
- Best for: AI-optimized search results
- Features: Advanced search depth, AI-summarized results
- Website: https://tavily.com

### Serper
- Best for: Google Search API alternative
- Features: Fast, affordable Google-like results
- Website: https://serper.dev

### Bing
- Best for: Microsoft ecosystem integration
- Features: Image search, news, video results
- Website: https://www.microsoft.com/en-us/bing/apis

### Google Custom Search
- Best for: Site-specific or custom search
- Features: Customizable search engines
- Website: https://programmablesearchengine.google.com

## Notes

- API keys should be stored securely using Terraform variables or vault integration
- Timeout values should account for network latency and provider response times
- Use max_retries to handle transient failures gracefully
- search_tool_info allows passing provider-specific options not covered by standard fields
- Consider rate limits when configuring multiple search tools
