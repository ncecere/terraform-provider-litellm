# Resource: litellm_router_config

Manages router configurations in LiteLLM for load balancing and failover strategies. This resource allows you to configure how LiteLLM routes requests across multiple models, handles failures, implements caching, and manages rate limits.

## Example Usage

### Basic Load Balancing Configuration

```hcl
resource "litellm_router_config" "main" {
  routing_strategy = "simple-shuffle"
  
  model_aliases = {
    "gpt-4"        = "gpt-4-0613"
    "gpt-3.5"      = "gpt-3.5-turbo-16k"
    "claude"       = "claude-3-opus-20240229"
  }
  
  fallback_chains = {
    "gpt-4"    = "gpt-3.5,claude"
    "gpt-3.5"  = "claude"
    "claude"   = "gpt-3.5"
  }
}
```

### Advanced Configuration with Retry and Timeout

```hcl
resource "litellm_router_config" "production" {
  routing_strategy = "least-busy"
  enabled          = true
  
  model_aliases = {
    "primary-model"   = "gpt-4-turbo"
    "fallback-model"  = "gpt-3.5-turbo"
    "budget-model"    = "gpt-3.5-turbo-16k"
  }
  
  fallback_chains = {
    "primary-model"  = "fallback-model,budget-model"
    "fallback-model" = "budget-model"
  }
  
  retry_config {
    num_retries           = 3
    retry_after           = 5
    retry_on_status_codes = [429, 500, 502, 503, 504]
  }
  
  timeout_config {
    request_timeout = 300  # 5 minutes
    stream_timeout  = 600  # 10 minutes
  }
  
  load_balancing_config {
    enable_health_checks  = true
    health_check_interval = 30
    failure_threshold     = 3
    success_threshold     = 2
  }
}
```

### Caching and Rate Limiting Configuration

```hcl
resource "litellm_router_config" "cached" {
  routing_strategy = "cost-based-routing"
  
  model_aliases = {
    "fast"      = "gpt-3.5-turbo"
    "accurate"  = "gpt-4"
    "economic"  = "gpt-3.5-turbo-16k"
  }
  
  fallback_chains = {
    "accurate" = "fast,economic"
    "fast"     = "economic"
  }
  
  cache_config {
    enable_cache   = true
    cache_ttl      = 3600      # 1 hour
    cache_size_mb  = 1000      # 1GB cache
  }
  
  rate_limit_config {
    enable_rate_limiting = true
    requests_per_minute  = 1000
    tokens_per_minute    = 1000000
  }
}
```

### Latency-Optimized Configuration

```hcl
resource "litellm_router_config" "low_latency" {
  routing_strategy = "latency-based-routing"
  
  model_aliases = {
    "fastest"  = "gpt-3.5-turbo"
    "balanced" = "gpt-4-turbo"
    "quality"  = "gpt-4"
  }
  
  # Shorter timeouts for latency-sensitive applications
  timeout_config {
    request_timeout = 30   # 30 seconds
    stream_timeout  = 120  # 2 minutes
  }
  
  # Aggressive retry for fast recovery
  retry_config {
    num_retries = 2
    retry_after = 1
    retry_on_status_codes = [429, 503]
  }
  
  # Frequent health checks for quick failure detection
  load_balancing_config {
    enable_health_checks  = true
    health_check_interval = 10
    failure_threshold     = 2
    success_threshold     = 1
  }
}
```

## Argument Reference

### Required Arguments

- `routing_strategy` (String) - Routing strategy for load balancing. Valid values:
  - `simple-shuffle` - Random distribution across available models
  - `least-busy` - Route to the model with the least active requests
  - `usage-based-routing` - Route based on usage patterns and quotas
  - `latency-based-routing` - Route to the model with the lowest latency
  - `cost-based-routing` - Route to the most cost-effective model

### Optional Arguments

- `model_aliases` (Map of String) - Map of model aliases to actual model names. Allows using simplified names in API calls.
- `fallback_chains` (Map of String) - Map of model names to their fallback chains. Value is a comma-separated list of fallback models.
- `enabled` (Boolean) - Whether this router configuration is enabled. Default: `true`.

#### retry_config Block

- `retry_config` (Block List, Max: 1) - Retry configuration for failed requests.
  - `num_retries` (Number) - Number of retry attempts (0-10). Default: `3`.
  - `retry_after` (Number) - Seconds to wait between retries (1-300). Default: `5`.
  - `retry_on_status_codes` (Set of Number) - HTTP status codes that trigger retries. Default includes common server errors.

#### timeout_config Block

- `timeout_config` (Block List, Max: 1) - Timeout configuration for requests.
  - `request_timeout` (Number) - Request timeout in seconds (1-3600). Default: `600`.
  - `stream_timeout` (Number) - Stream timeout in seconds (1-7200). Default: `1800`.

#### load_balancing_config Block

- `load_balancing_config` (Block List, Max: 1) - Load balancing configuration.
  - `enable_health_checks` (Boolean) - Enable health checks for models. Default: `true`.
  - `health_check_interval` (Number) - Health check interval in seconds (10-300). Default: `30`.
  - `failure_threshold` (Number) - Number of failures before marking unhealthy (1-10). Default: `3`.
  - `success_threshold` (Number) - Number of successes before marking healthy (1-10). Default: `2`.

#### cache_config Block

- `cache_config` (Block List, Max: 1) - Caching configuration.
  - `enable_cache` (Boolean) - Enable response caching. Default: `false`.
  - `cache_ttl` (Number) - Cache TTL in seconds (60-86400). Default: `3600`.
  - `cache_size_mb` (Number) - Maximum cache size in MB (10-10000). Default: `100`.

#### rate_limit_config Block

- `rate_limit_config` (Block List, Max: 1) - Rate limiting configuration.
  - `enable_rate_limiting` (Boolean) - Enable rate limiting. Default: `false`.
  - `requests_per_minute` (Number) - Requests per minute limit (1-100000). Default: `1000`.
  - `tokens_per_minute` (Number) - Tokens per minute limit (1-10000000). Default: `100000`.

## Attributes Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier of the router configuration.

## Import

Router configurations can be imported using their ID:

```bash
terraform import litellm_router_config.example router-config-id
```

## Use Cases

### 1. Multi-Region Deployment

```hcl
locals {
  regions = {
    us_east = ["gpt-4-us-east", "claude-3-us-east"]
    eu_west = ["gpt-4-eu-west", "claude-3-eu-west"]
    asia    = ["gpt-4-asia", "claude-3-asia"]
  }
}

resource "litellm_router_config" "regional" {
  routing_strategy = "latency-based-routing"
  
  model_aliases = {
    for region, models in local.regions :
    "model-${region}" => models[0]
  }
  
  fallback_chains = {
    for region, models in local.regions :
    models[0] => join(",", slice(models, 1, length(models)))
  }
  
  load_balancing_config {
    enable_health_checks  = true
    health_check_interval = 20
    failure_threshold     = 2
    success_threshold     = 2
  }
}
```

### 2. Cost-Optimized Routing

```hcl
resource "litellm_router_config" "cost_optimized" {
  routing_strategy = "cost-based-routing"
  
  model_aliases = {
    "premium"  = "gpt-4"
    "standard" = "gpt-3.5-turbo"
    "budget"   = "gpt-3.5-turbo-16k"
  }
  
  # Use cheaper models as fallbacks
  fallback_chains = {
    "premium"  = "standard,budget"
    "standard" = "budget"
  }
  
  # Cache to reduce costs
  cache_config {
    enable_cache  = true
    cache_ttl     = 7200  # 2 hours
    cache_size_mb = 5000  # 5GB
  }
  
  # Rate limits to control costs
  rate_limit_config {
    enable_rate_limiting = true
    requests_per_minute  = 500
    tokens_per_minute    = 500000
  }
}
```

### 3. High Availability Configuration

```hcl
resource "litellm_router_config" "high_availability" {
  routing_strategy = "least-busy"
  
  model_aliases = {
    "primary-1"   = "gpt-4-deployment-1"
    "primary-2"   = "gpt-4-deployment-2"
    "secondary-1" = "gpt-3.5-deployment-1"
    "secondary-2" = "gpt-3.5-deployment-2"
  }
  
  fallback_chains = {
    "primary-1"   = "primary-2,secondary-1,secondary-2"
    "primary-2"   = "primary-1,secondary-1,secondary-2"
    "secondary-1" = "secondary-2,primary-1,primary-2"
    "secondary-2" = "secondary-1,primary-1,primary-2"
  }
  
  retry_config {
    num_retries           = 5
    retry_after           = 2
    retry_on_status_codes = [429, 500, 502, 503, 504]
  }
  
  load_balancing_config {
    enable_health_checks  = true
    health_check_interval = 15
    failure_threshold     = 2
    success_threshold     = 1
  }
}
```

### 4. Development vs Production

```hcl
locals {
  is_production = terraform.workspace == "prod"
}

resource "litellm_router_config" "environment_specific" {
  routing_strategy = local.is_production ? "latency-based-routing" : "simple-shuffle"
  
  model_aliases = {
    "default" = local.is_production ? "gpt-4" : "gpt-3.5-turbo"
  }
  
  retry_config {
    num_retries = local.is_production ? 3 : 1
    retry_after = local.is_production ? 5 : 10
  }
  
  cache_config {
    enable_cache  = local.is_production
    cache_ttl     = 3600
    cache_size_mb = local.is_production ? 5000 : 100
  }
  
  rate_limit_config {
    enable_rate_limiting = !local.is_production
    requests_per_minute  = local.is_production ? 10000 : 100
    tokens_per_minute    = local.is_production ? 10000000 : 100000
  }
}
```

## Notes

- Router configurations affect all requests through the LiteLLM proxy.
- Only one router configuration should be active at a time.
- Model aliases allow for abstraction - clients can use logical names while the router handles actual model selection.
- Fallback chains are processed in order - the first available model is used.
- Health checks run in the background and don't affect request latency.
- Cache keys are generated based on request content - identical requests return cached responses.
- Rate limits are applied per LiteLLM instance - consider this in multi-instance deployments.
- The routing strategy significantly impacts performance and cost:
  - Use `simple-shuffle` for even distribution
  - Use `least-busy` for optimal concurrency
  - Use `latency-based-routing` for best response times
  - Use `cost-based-routing` for cost optimization
  - Use `usage-based-routing` for quota management