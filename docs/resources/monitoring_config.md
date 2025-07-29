# Resource: litellm_monitoring_config

Manages monitoring and callback configurations in LiteLLM for metrics and observability. This resource allows you to configure various monitoring integrations including Prometheus, Datadog, OpenTelemetry, Langfuse, and custom webhooks.

## Example Usage

### Prometheus Configuration

```hcl
resource "litellm_monitoring_config" "prometheus" {
  callback_type = "prometheus"
  endpoint      = "http://prometheus:9090/metrics"
  enabled       = true
  
  enabled_metrics = [
    "requests",
    "latency",
    "errors",
    "costs",
    "tokens",
    "model_usage",
    "user_usage"
  ]
  
  labels = {
    environment = "production"
    service     = "litellm-proxy"
    region      = "us-east-1"
  }
  
  metric_prefix = "litellm"
  sampling_rate = 1.0  # Capture all requests
}
```

### Datadog Integration

```hcl
resource "litellm_monitoring_config" "datadog" {
  callback_type = "datadog"
  endpoint      = "https://api.datadoghq.com/api/v2/series"
  
  enabled_metrics = [
    "requests",
    "latency",
    "errors",
    "costs"
  ]
  
  auth_config = {
    api_key = var.datadog_api_key
    app_key = var.datadog_app_key
  }
  
  labels = {
    env     = terraform.workspace
    service = "litellm"
    version = var.app_version
  }
  
  batch_size     = 500
  flush_interval = 30  # Flush every 30 seconds
}
```

### OpenTelemetry (OTEL) Configuration

```hcl
resource "litellm_monitoring_config" "otel" {
  callback_type = "otel"
  endpoint      = "http://otel-collector:4317"
  
  enabled_metrics = [
    "requests",
    "latency",
    "errors",
    "tokens",
    "model_usage",
    "team_usage"
  ]
  
  labels = {
    service_name    = "litellm-proxy"
    service_version = var.service_version
    deployment      = "kubernetes"
  }
  
  metric_prefix = "litellm"
  
  # OTEL specific headers
  custom_headers = {
    "otel-service-name" = "litellm-proxy"
  }
  
  include_request_details  = true
  include_response_details = false
}
```

### Langfuse Integration

```hcl
resource "litellm_monitoring_config" "langfuse" {
  callback_type = "langfuse"
  endpoint      = "https://cloud.langfuse.com"
  
  enabled_metrics = [
    "requests",
    "costs",
    "tokens",
    "model_usage"
  ]
  
  auth_config = {
    public_key  = var.langfuse_public_key
    secret_key  = var.langfuse_secret_key
  }
  
  sampling_rate = 0.1  # Sample 10% of requests
  
  include_request_details  = true
  include_response_details = true
}
```

### Custom Webhook

```hcl
resource "litellm_monitoring_config" "custom_webhook" {
  callback_type = "custom_webhook"
  endpoint      = "https://analytics.company.com/litellm/metrics"
  
  enabled_metrics = [
    "requests",
    "errors",
    "costs",
    "user_usage",
    "key_usage"
  ]
  
  auth_config = {
    bearer_token = var.webhook_auth_token
  }
  
  custom_headers = {
    "X-Service-Name"    = "litellm"
    "X-Environment"     = terraform.workspace
    "X-Webhook-Version" = "v2"
  }
  
  batch_size     = 100
  flush_interval = 60
  
  labels = {
    datacenter = var.datacenter
    cluster    = var.cluster_name
  }
}
```

## Argument Reference

### Required Arguments

- `callback_type` (String) - Type of monitoring callback. Valid values:
  - `prometheus` - Prometheus metrics exporter
  - `datadog` - Datadog metrics integration
  - `otel` - OpenTelemetry integration
  - `langfuse` - Langfuse observability platform
  - `custom_webhook` - Custom webhook endpoint

### Optional Arguments

- `endpoint` (String) - Endpoint URL for the monitoring service. Required for most callback types except Prometheus (which can use a default endpoint).
- `enabled` (Boolean) - Whether this monitoring configuration is enabled. Default: `true`.
- `enabled_metrics` (Set of String) - List of metrics to enable. Valid values:
  - `requests` - Request count metrics
  - `latency` - Request latency metrics
  - `errors` - Error rate metrics
  - `costs` - Cost tracking metrics
  - `tokens` - Token usage metrics
  - `model_usage` - Model-specific usage metrics
  - `user_usage` - User-specific usage metrics
  - `team_usage` - Team-specific usage metrics
  - `key_usage` - API key usage metrics
- `labels` (Map of String) - Labels to add to all metrics. Useful for filtering and grouping in monitoring systems.
- `sampling_rate` (Number) - Sampling rate for metrics (0.0 to 1.0). Default: `1.0` (capture all requests).
- `batch_size` (Number) - Batch size for metric exports (1-10000). Default: `100`.
- `flush_interval` (Number) - Flush interval in seconds (1-3600). Default: `60`.
- `auth_config` (Map of String, Sensitive) - Authentication configuration for the monitoring endpoint. Common fields:
  - `api_key` - API key for authentication
  - `bearer_token` - Bearer token for authentication
  - `username`/`password` - Basic auth credentials
- `custom_headers` (Map of String) - Custom headers to include in monitoring requests.
- `metric_prefix` (String) - Prefix for all metric names. Default: `"litellm"`.
- `include_request_details` (Boolean) - Include detailed request information in metrics. Default: `false`.
- `include_response_details` (Boolean) - Include detailed response information in metrics. Default: `false`.

## Attributes Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier of the monitoring configuration.

## Import

Monitoring configurations can be imported using their ID:

```bash
terraform import litellm_monitoring_config.example monitoring-prometheus
```

## Use Cases

### 1. Multi-Environment Monitoring

```hcl
locals {
  environments = {
    dev = {
      endpoint      = "http://prometheus-dev:9090/metrics"
      sampling_rate = 0.1
    }
    staging = {
      endpoint      = "http://prometheus-staging:9090/metrics"
      sampling_rate = 0.5
    }
    prod = {
      endpoint      = "http://prometheus-prod:9090/metrics"
      sampling_rate = 1.0
    }
  }
}

resource "litellm_monitoring_config" "env_monitoring" {
  callback_type = "prometheus"
  endpoint      = local.environments[terraform.workspace].endpoint
  
  enabled_metrics = [
    "requests", "latency", "errors", "costs"
  ]
  
  labels = {
    environment = terraform.workspace
    service     = "litellm-proxy"
  }
  
  sampling_rate = local.environments[terraform.workspace].sampling_rate
}
```

### 2. Comprehensive Observability Stack

```hcl
# Metrics to Prometheus
resource "litellm_monitoring_config" "metrics" {
  callback_type = "prometheus"
  
  enabled_metrics = [
    "requests", "latency", "errors", "costs", "tokens"
  ]
  
  labels = {
    component = "api-gateway"
  }
}

# Traces to OTEL
resource "litellm_monitoring_config" "traces" {
  callback_type = "otel"
  endpoint      = var.otel_endpoint
  
  enabled_metrics = [
    "requests", "latency", "model_usage"
  ]
  
  include_request_details  = true
  include_response_details = true
}

# Business metrics to custom analytics
resource "litellm_monitoring_config" "analytics" {
  callback_type = "custom_webhook"
  endpoint      = var.analytics_endpoint
  
  enabled_metrics = [
    "costs", "user_usage", "team_usage"
  ]
  
  auth_config = {
    api_key = var.analytics_api_key
  }
  
  batch_size = 1000
}
```

### 3. Cost-Optimized Monitoring

```hcl
# High-frequency metrics with sampling
resource "litellm_monitoring_config" "high_freq_metrics" {
  callback_type = "prometheus"
  
  enabled_metrics = [
    "requests", "latency"
  ]
  
  sampling_rate = 0.01  # Sample 1% for high-volume metrics
}

# Critical metrics without sampling
resource "litellm_monitoring_config" "critical_metrics" {
  callback_type = "datadog"
  endpoint      = var.datadog_endpoint
  
  enabled_metrics = [
    "errors", "costs"
  ]
  
  auth_config = {
    api_key = var.datadog_api_key
  }
  
  sampling_rate = 1.0  # Capture all critical events
  
  labels = {
    alert_priority = "high"
  }
}
```

### 4. Debugging Configuration

```hcl
variable "debug_mode" {
  type    = bool
  default = false
}

resource "litellm_monitoring_config" "debug" {
  count = var.debug_mode ? 1 : 0
  
  callback_type = "custom_webhook"
  endpoint      = var.debug_webhook_endpoint
  
  enabled_metrics = [
    "requests", "latency", "errors", "tokens", "model_usage"
  ]
  
  # Full details for debugging
  include_request_details  = true
  include_response_details = true
  
  # No sampling in debug mode
  sampling_rate = 1.0
  
  labels = {
    debug_session = var.debug_session_id
    debug_user    = var.debug_user
  }
}
```

## Notes

- Multiple monitoring configurations can be active simultaneously for different purposes.
- The `sampling_rate` helps control costs and data volume - use lower rates for high-traffic environments.
- `include_request_details` and `include_response_details` can significantly increase data volume - use with caution.
- Authentication credentials in `auth_config` are stored securely and never exposed in logs or state.
- Some callback types have specific requirements:
  - Prometheus: Typically uses a pull model, so endpoint might be where LiteLLM exposes metrics
  - Datadog: Requires valid API and app keys
  - OTEL: Requires a compatible OpenTelemetry collector
  - Langfuse: Requires valid public and secret keys
- Batch settings (`batch_size` and `flush_interval`) help optimize network usage and monitoring system load.