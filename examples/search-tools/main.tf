# Search Tools Example
# This example demonstrates configuring various search tool providers

terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {}

# =============================================================================
# SEARCH TOOLS - MINIMAL EXAMPLES
# =============================================================================

# Minimal Tavily search
resource "litellm_search_tool" "tavily_minimal" {
  search_tool_name = "tavily-basic"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
}

# Minimal Serper search
resource "litellm_search_tool" "serper_minimal" {
  search_tool_name = "serper-basic"
  search_provider  = "serper"
  api_key          = var.serper_api_key
}

# =============================================================================
# SEARCH TOOLS - FULL EXAMPLES
# =============================================================================

# Full Tavily configuration
resource "litellm_search_tool" "tavily_advanced" {
  search_tool_name = "tavily-advanced"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
  api_base         = "https://api.tavily.com"
  timeout          = 30.0
  max_retries      = 3

  search_tool_info = jsonencode({
    search_depth        = "advanced"
    max_results         = 10
    include_images      = true
    include_answer      = true
    include_raw_content = false
  })
}

# Full Serper configuration
resource "litellm_search_tool" "serper_advanced" {
  search_tool_name = "serper-google-advanced"
  search_provider  = "serper"
  api_key          = var.serper_api_key
  timeout          = 15.0
  max_retries      = 2

  search_tool_info = jsonencode({
    gl          = "us"
    hl          = "en"
    num         = 10
    type        = "search"
    autocorrect = true
  })
}

# Bing search configuration
resource "litellm_search_tool" "bing" {
  search_tool_name = "bing-web-search"
  search_provider  = "bing"
  api_key          = var.bing_api_key
  api_base         = "https://api.bing.microsoft.com/v7.0"
  timeout          = 20.0
  max_retries      = 3

  search_tool_info = jsonencode({
    mkt        = "en-US"
    safeSearch = "Moderate"
    count      = 10
    freshness  = "Month"
  })
}

# Google Custom Search configuration
resource "litellm_search_tool" "google" {
  search_tool_name = "google-custom-search"
  search_provider  = "google"
  api_key          = var.google_api_key
  timeout          = 15.0
  max_retries      = 2

  search_tool_info = jsonencode({
    cx           = var.google_search_engine_id
    safe         = "active"
    num          = 10
    dateRestrict = "m1"
    lr           = "lang_en"
  })
}

# =============================================================================
# USE CASE: PRIMARY + FALLBACK SEARCH
# =============================================================================

# Primary search tool with high reliability settings
resource "litellm_search_tool" "primary_search" {
  search_tool_name = "primary-search"
  search_provider  = "tavily"
  api_key          = var.tavily_api_key
  timeout          = 30.0
  max_retries      = 5

  search_tool_info = jsonencode({
    description  = "Primary search tool for production use"
    priority     = "high"
    search_depth = "advanced"
    max_results  = 10
  })
}

# Fallback search tool with faster timeout
resource "litellm_search_tool" "fallback_search" {
  search_tool_name = "fallback-search"
  search_provider  = "serper"
  api_key          = var.serper_api_key
  timeout          = 10.0
  max_retries      = 2

  search_tool_info = jsonencode({
    description = "Fallback search tool when primary is unavailable"
    priority    = "low"
    num         = 5
  })
}

# =============================================================================
# VARIABLES
# =============================================================================

variable "tavily_api_key" {
  description = "Tavily API key"
  type        = string
  sensitive   = true
}

variable "serper_api_key" {
  description = "Serper API key"
  type        = string
  sensitive   = true
}

variable "bing_api_key" {
  description = "Bing Search API key"
  type        = string
  sensitive   = true
}

variable "google_api_key" {
  description = "Google Custom Search API key"
  type        = string
  sensitive   = true
}

variable "google_search_engine_id" {
  description = "Google Custom Search Engine ID"
  type        = string
}

# =============================================================================
# OUTPUTS
# =============================================================================

output "search_tool_ids" {
  value = {
    tavily_basic    = litellm_search_tool.tavily_minimal.search_tool_id
    tavily_advanced = litellm_search_tool.tavily_advanced.search_tool_id
    serper_basic    = litellm_search_tool.serper_minimal.search_tool_id
    serper_advanced = litellm_search_tool.serper_advanced.search_tool_id
    bing            = litellm_search_tool.bing.search_tool_id
    google          = litellm_search_tool.google.search_tool_id
    primary         = litellm_search_tool.primary_search.search_tool_id
    fallback        = litellm_search_tool.fallback_search.search_tool_id
  }
}
