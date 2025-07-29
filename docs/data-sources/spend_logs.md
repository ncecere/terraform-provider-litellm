# Data Source: litellm_spend_logs

Queries LiteLLM spend logs and usage analytics. This data source allows you to retrieve detailed spending information, aggregate usage data, and analyze costs across different dimensions.

## Example Usage

### Basic Usage - Monthly Report

```hcl
data "litellm_spend_logs" "monthly_usage" {
  start_date = "2024-01-01T00:00:00Z"
  end_date   = "2024-01-31T23:59:59Z"
  
  filters = {
    team_id = litellm_team.engineering.id
  }
}

output "monthly_spend" {
  value = data.litellm_spend_logs.monthly_usage.total_spend
}

output "monthly_tokens" {
  value = data.litellm_spend_logs.monthly_usage.total_tokens
}
```

### Aggregated by Model

```hcl
data "litellm_spend_logs" "model_usage" {
  start_date  = formatdate("YYYY-MM-DD'T'00:00:00Z", timestamp())
  end_date    = formatdate("YYYY-MM-DD'T'23:59:59Z", timeadd(timestamp(), "24h"))
  aggregation = "by_model"
  
  filters = {
    team_id = litellm_team.engineering.id
  }
}

# Display model usage breakdown
output "model_breakdown" {
  value = {
    for item in data.litellm_spend_logs.model_usage.aggregated_data :
    item.dimension => {
      spend    = item.total_spend
      requests = item.total_requests
      tokens   = item.total_tokens
    }
  }
}
```

### Daily Usage Trend

```hcl
data "litellm_spend_logs" "daily_trend" {
  start_date  = formatdate("YYYY-MM-DD'T'00:00:00Z", timeadd(timestamp(), "-7d"))
  end_date    = formatdate("YYYY-MM-DD'T'23:59:59Z", timestamp())
  aggregation = "daily"
  
  filters = {
    model = "gpt-4"
  }
}

# Create CloudWatch metric for daily spend
resource "aws_cloudwatch_metric_alarm" "high_daily_spend" {
  for_each = {
    for day in data.litellm_spend_logs.daily_trend.aggregated_data :
    day.period => day if day.total_spend > 100
  }
  
  alarm_name          = "high-litellm-spend-${each.key}"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "1"
  metric_name         = "LiteLLMDailySpend"
  namespace           = "CustomMetrics"
  period              = "86400"
  statistic           = "Sum"
  threshold           = "100"
  alarm_description   = "LiteLLM daily spend exceeded $100 on ${each.key}"
}
```

### User Activity Analysis

```hcl
data "litellm_spend_logs" "user_activity" {
  start_date  = var.report_start_date
  end_date    = var.report_end_date
  aggregation = "by_user"
  
  filters = {
    team_id = litellm_team.engineering.id
  }
  
  include_metadata = true
}

# Generate user activity report
locals {
  user_report = {
    for user in data.litellm_spend_logs.user_activity.aggregated_data :
    user.dimension => {
      total_spend   = user.total_spend
      total_requests = user.total_requests
      avg_cost      = user.average_cost_per_request
      efficiency    = user.total_tokens > 0 ? user.total_spend / user.total_tokens * 1000 : 0
    }
  }
}
```

### Detailed Log Analysis

```hcl
data "litellm_spend_logs" "detailed_logs" {
  start_date = formatdate("YYYY-MM-DD'T'HH:00:00Z", timeadd(timestamp(), "-1h"))
  end_date   = formatdate("YYYY-MM-DD'T'HH:mm:ssZ", timestamp())
  
  filters = {
    user_id = "suspicious-user"
    model   = "gpt-4"
  }
  
  include_metadata = true
}

# Check for anomalous usage
output "high_cost_requests" {
  value = [
    for log in data.litellm_spend_logs.detailed_logs.logs :
    {
      request_id = log.request_id
      cost       = log.spend
      tokens     = log.total_tokens
      timestamp  = log.created_at
    }
    if log.spend > 1.0  # Requests costing more than $1
  ]
}
```

## Argument Reference

### Required Arguments

- `start_date` (String) - Start date for the query in RFC3339 format (e.g., "2024-01-01T00:00:00Z").
- `end_date` (String) - End date for the query in RFC3339 format (e.g., "2024-01-31T23:59:59Z").

### Optional Arguments

- `filters` (Map of String) - Filters to apply to the query. Available filters:
  - `team_id` - Filter by team ID
  - `user_id` - Filter by user ID
  - `model` - Filter by model name
  - `api_key` - Filter by API key (prefix match)
  - `request_id` - Filter by specific request ID
- `aggregation` (String) - Aggregation type. Default: `"none"`. Valid values:
  - `none` - Return individual log entries
  - `daily` - Aggregate by day
  - `weekly` - Aggregate by week
  - `monthly` - Aggregate by month
  - `by_user` - Aggregate by user
  - `by_team` - Aggregate by team
  - `by_model` - Aggregate by model
- `include_metadata` (Boolean) - Include detailed metadata in results. Default: `false`.

## Attributes Reference

The following attributes are exported:

### Summary Attributes

- `total_spend` (Number) - Total spend for the period in USD.
- `total_tokens` (Number) - Total tokens used for the period.
- `total_requests` (Number) - Total number of requests for the period.

### Detailed Logs

- `logs` (List of Object) - Detailed spend logs (when aggregation is "none"):
  - `request_id` (String) - Unique request ID
  - `user_id` (String) - User ID who made the request
  - `team_id` (String) - Team ID associated with the request
  - `api_key` (String) - API key used (masked for security)
  - `model` (String) - Model used for the request
  - `spend` (Number) - Cost of the request in USD
  - `total_tokens` (Number) - Total tokens used
  - `prompt_tokens` (Number) - Prompt tokens used
  - `completion_tokens` (Number) - Completion tokens used
  - `created_at` (String) - Timestamp of the request
  - `metadata` (Map of String) - Additional metadata (if include_metadata is true)

### Aggregated Data

- `aggregated_data` (List of Object) - Aggregated spend data (when aggregation is enabled):
  - `period` (String) - Period for aggregation (for time-based aggregations)
  - `dimension` (String) - Dimension value (user_id, team_id, model, etc.)
  - `total_spend` (Number) - Total spend for this dimension/period
  - `total_tokens` (Number) - Total tokens for this dimension/period
  - `total_requests` (Number) - Total requests for this dimension/period
  - `average_cost_per_request` (Number) - Average cost per request

## Use Cases

### 1. Cost Allocation and Chargeback

```hcl
# Get spend by team for chargeback
data "litellm_spend_logs" "team_chargeback" {
  start_date  = var.billing_period_start
  end_date    = var.billing_period_end
  aggregation = "by_team"
}

# Generate chargeback report
resource "local_file" "chargeback_report" {
  filename = "chargeback_${formatdate("YYYY-MM", var.billing_period_start)}.json"
  content = jsonencode({
    period = {
      start = var.billing_period_start
      end   = var.billing_period_end
    }
    teams = {
      for item in data.litellm_spend_logs.team_chargeback.aggregated_data :
      item.dimension => {
        total_cost     = item.total_spend
        total_requests = item.total_requests
        cost_per_request = item.average_cost_per_request
      }
    }
    total = data.litellm_spend_logs.team_chargeback.total_spend
  })
}
```

### 2. Anomaly Detection

```hcl
# Get hourly usage for anomaly detection
locals {
  hours = [for i in range(24) : formatdate("YYYY-MM-DD'T'%02d:00:00Z", timestamp(), i)]
}

data "litellm_spend_logs" "hourly_usage" {
  for_each = toset(local.hours)
  
  start_date = each.value
  end_date   = timeadd(each.value, "1h")
  
  filters = {
    team_id = litellm_team.production.id
  }
}

# Flag anomalous hours
output "anomalous_hours" {
  value = {
    for hour, data in data.litellm_spend_logs.hourly_usage :
    hour => data.total_spend
    if data.total_spend > 50  # More than $50 in one hour
  }
}
```

### 3. Model Performance Comparison

```hcl
# Compare different models
variable "models_to_compare" {
  default = ["gpt-4", "gpt-3.5-turbo", "claude-3-opus"]
}

data "litellm_spend_logs" "model_comparison" {
  for_each = toset(var.models_to_compare)
  
  start_date = var.comparison_start
  end_date   = var.comparison_end
  
  filters = {
    model = each.value
  }
}

output "model_efficiency" {
  value = {
    for model, data in data.litellm_spend_logs.model_comparison :
    model => {
      total_cost   = data.total_spend
      total_tokens = data.total_tokens
      cost_per_1k_tokens = data.total_tokens > 0 ? (data.total_spend / data.total_tokens) * 1000 : 0
      requests     = data.total_requests
    }
  }
}
```

### 4. Budget Tracking Dashboard

```hcl
# Current month spend tracking
data "litellm_spend_logs" "current_month" {
  start_date = formatdate("YYYY-MM-01T00:00:00Z", timestamp())
  end_date   = timestamp()
  
  aggregation = "by_team"
}

# Previous month for comparison
data "litellm_spend_logs" "previous_month" {
  start_date = formatdate("YYYY-MM-01T00:00:00Z", timeadd(timestamp(), "-1m"))
  end_date   = formatdate("YYYY-MM-01T00:00:00Z", timestamp())
  
  aggregation = "by_team"
}

output "budget_status" {
  value = {
    current_spend = data.litellm_spend_logs.current_month.total_spend
    previous_month = data.litellm_spend_logs.previous_month.total_spend
    trend = data.litellm_spend_logs.current_month.total_spend > data.litellm_spend_logs.previous_month.total_spend ? "increasing" : "decreasing"
    by_team = {
      for item in data.litellm_spend_logs.current_month.aggregated_data :
      item.dimension => item.total_spend
    }
  }
}
```

## Notes

- The data source respects LiteLLM's data retention policies - older data may not be available.
- API keys in the response are masked for security (showing only first 4 and last 4 characters).
- Large date ranges with `aggregation = "none"` may return truncated results - use aggregation for summary data.
- Timestamps in responses are in UTC.
- The `include_metadata` flag may significantly increase response size - use only when needed.
- Filters are combined with AND logic - all specified filters must match.