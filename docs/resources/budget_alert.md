# Resource: litellm_budget_alert

Manages budget alerts in LiteLLM for monitoring spending thresholds. This resource allows you to configure alerts at various scopes (user, team, global, or key level) to notify when spending approaches or exceeds defined thresholds.

## Example Usage

### Team Budget Alert

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
  max_budget = 1000.0
}

resource "litellm_budget_alert" "team_budget_warning" {
  alert_name        = "Engineering Team 80% Budget Alert"
  threshold_percent = 80.0
  budget_scope      = "team"
  scope_id          = litellm_team.engineering.id
  
  notification_channels = [
    "email:team-lead@company.com",
    "email:finance@company.com"
  ]
  
  alert_frequency       = "daily"
  include_usage_details = true
}
```

### Multiple Threshold Alerts

```hcl
# 50% warning
resource "litellm_budget_alert" "budget_warning_50" {
  alert_name        = "Budget 50% Warning"
  threshold_percent = 50.0
  budget_scope      = "team"
  scope_id          = litellm_team.engineering.id
  
  notification_channels = [
    "email:team-lead@company.com"
  ]
  
  alert_frequency = "once"
}

# 80% warning
resource "litellm_budget_alert" "budget_warning_80" {
  alert_name        = "Budget 80% Warning"
  threshold_percent = 80.0
  budget_scope      = "team"
  scope_id          = litellm_team.engineering.id
  
  notification_channels = [
    "email:team-lead@company.com",
    "webhook:https://api.company.com/budget-alerts"
  ]
  
  alert_frequency = "daily"
}

# 100% critical alert
resource "litellm_budget_alert" "budget_critical" {
  alert_name        = "Budget Exceeded"
  threshold_percent = 100.0
  budget_scope      = "team"
  scope_id          = litellm_team.engineering.id
  
  notification_channels = [
    "email:team-lead@company.com",
    "email:cto@company.com",
    "webhook:https://api.company.com/critical-alerts"
  ]
  
  alert_frequency = "always"
}
```

### User-Level Budget Alert

```hcl
resource "litellm_budget_alert" "user_budget" {
  alert_name        = "User Budget Alert"
  threshold_percent = 90.0
  budget_scope      = "user"
  scope_id          = "user-123"
  
  notification_channels = [
    "email:user@company.com"
  ]
  
  include_usage_details = true
  
  metadata = {
    department    = "engineering"
    cost_center   = "CC-123"
    alert_version = "v1"
  }
}
```

### Global Budget Alert

```hcl
resource "litellm_budget_alert" "global_alert" {
  alert_name        = "Global Spending Alert"
  threshold_percent = 75.0
  budget_scope      = "global"
  # scope_id not required for global scope
  
  notification_channels = [
    "email:finance@company.com",
    "webhook:https://monitoring.company.com/webhook/litellm"
  ]
  
  alert_frequency = "weekly"
}
```

### API Key Budget Alert

```hcl
resource "litellm_api_key_enhanced" "service_key" {
  key_alias = "production-service"
  
  permissions {
    max_budget = 500.0
  }
}

resource "litellm_budget_alert" "key_alert" {
  alert_name        = "API Key Budget Alert"
  threshold_percent = 95.0
  budget_scope      = "key"
  scope_id          = litellm_api_key_enhanced.service_key.key_hash
  
  notification_channels = [
    "email:devops@company.com",
    "webhook:${var.pagerduty_webhook}"
  ]
  
  alert_frequency = "always"
}
```

## Argument Reference

### Required Arguments

- `alert_name` (String) - Name of the budget alert. Must be unique within the scope.
- `threshold_percent` (Number) - Percentage of budget that triggers the alert (0-100). For example, 80.0 means alert when 80% of budget is consumed.
- `budget_scope` (String) - Scope of the budget alert. Valid values:
  - `user` - Alert on individual user budgets
  - `team` - Alert on team budgets
  - `global` - Alert on global/organization budget
  - `key` - Alert on API key budgets
- `notification_channels` (List of String) - List of notification channels. Format: `type:target`. Supported types:
  - `email:address@example.com` - Email notifications
  - `webhook:https://example.com/webhook` - Webhook notifications
  - `slack:channel-id` - Slack notifications (if configured)

### Optional Arguments

- `scope_id` (String) - ID of the user, team, or key. Required for non-global scopes. Not used for global scope.
- `enabled` (Boolean) - Whether this alert is enabled. Default: `true`.
- `alert_frequency` (String) - How often to send alerts. Default: `"once"`. Valid values:
  - `once` - Send alert only once when threshold is crossed
  - `daily` - Send alert once per day while over threshold
  - `weekly` - Send alert once per week while over threshold
  - `always` - Send alert on every API call while over threshold
- `include_usage_details` (Boolean) - Include detailed usage breakdown in alert notifications. Default: `true`.
- `metadata` (Map of String) - Additional metadata for the alert. Useful for tagging and organizing alerts.

## Attributes Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier of the budget alert.

## Import

Budget alerts can be imported using their ID:

```bash
terraform import litellm_budget_alert.example alert-id
```

## Use Cases

### 1. Tiered Alert System

```hcl
locals {
  alert_thresholds = [50, 75, 90, 100]
  team_id          = litellm_team.main.id
}

resource "litellm_budget_alert" "tiered_alerts" {
  for_each = toset([for t in local.alert_thresholds : tostring(t)])
  
  alert_name        = "Budget Alert ${each.value}%"
  threshold_percent = tonumber(each.value)
  budget_scope      = "team"
  scope_id          = local.team_id
  
  notification_channels = concat(
    ["email:team-lead@company.com"],
    tonumber(each.value) >= 90 ? ["email:manager@company.com"] : [],
    tonumber(each.value) == 100 ? ["webhook:${var.critical_webhook}"] : []
  )
  
  alert_frequency = tonumber(each.value) >= 90 ? "always" : "daily"
}
```

### 2. Department-Wide Monitoring

```hcl
variable "departments" {
  type = map(object({
    team_id     = string
    manager_email = string
    budget      = number
  }))
}

resource "litellm_budget_alert" "department_alerts" {
  for_each = var.departments
  
  alert_name        = "${each.key} Department Budget Alert"
  threshold_percent = 85.0
  budget_scope      = "team"
  scope_id          = each.value.team_id
  
  notification_channels = [
    "email:${each.value.manager_email}",
    "email:finance@company.com"
  ]
  
  metadata = {
    department = each.key
    budget     = tostring(each.value.budget)
  }
}
```

### 3. Environment-Specific Alerts

```hcl
locals {
  environment = terraform.workspace
  
  alert_config = {
    dev = {
      threshold = 90
      channels  = ["email:dev-team@company.com"]
      frequency = "weekly"
    }
    staging = {
      threshold = 80
      channels  = ["email:qa-team@company.com", "email:dev-team@company.com"]
      frequency = "daily"
    }
    prod = {
      threshold = 70
      channels  = ["email:ops@company.com", "webhook:${var.pagerduty_webhook}"]
      frequency = "always"
    }
  }
}

resource "litellm_budget_alert" "env_alert" {
  alert_name        = "${local.environment} Environment Budget Alert"
  threshold_percent = local.alert_config[local.environment].threshold
  budget_scope      = "global"
  
  notification_channels = local.alert_config[local.environment].channels
  alert_frequency       = local.alert_config[local.environment].frequency
  
  metadata = {
    environment = local.environment
  }
}
```

## Notes

- Budget alerts are evaluated in real-time as API usage occurs.
- The `alert_frequency` setting helps prevent alert fatigue - use `"once"` for non-critical thresholds.
- Webhook notifications should return 2xx status codes; failed webhooks are retried with exponential backoff.
- Multiple alerts can be configured for the same scope with different thresholds.
- Alerts respect the budget period configured at the scope level (e.g., monthly team budgets).
- When `include_usage_details` is true, alerts include breakdowns by model, user, and time period.