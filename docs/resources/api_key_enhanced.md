# Resource: litellm_api_key_enhanced

Enhanced API key management in LiteLLM with advanced permissions and lifecycle management. This resource provides comprehensive control over API keys including model restrictions, budget limits, rate limiting, automatic renewal, and detailed usage tracking.

## Example Usage

### Basic API Key

```hcl
resource "litellm_api_key_enhanced" "service_key" {
  key_alias = "production-service"
  user_id   = "service-account"
  
  permissions {
    models = ["gpt-4", "gpt-3.5-turbo"]
    max_budget = 100.0
    budget_duration = "1mo"
  }
  
  expires_at = "2024-12-31T23:59:59Z"
}

# Output the key (only available on create)
output "api_key" {
  value     = litellm_api_key_enhanced.service_key.key
  sensitive = true
}
```

### Team API Key with Rate Limits

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
  max_budget = 1000.0
}

resource "litellm_api_key_enhanced" "team_key" {
  key_alias = "engineering-team-key"
  team_id   = litellm_team.engineering.id
  
  permissions {
    models = ["gpt-4", "gpt-3.5-turbo", "claude-3-opus"]
    max_budget = 500.0
    budget_duration = "1mo"
    max_parallel_requests = 50
    
    allowed_endpoints = [
      "/v1/chat/completions",
      "/v1/embeddings"
    ]
  }
  
  rate_limits {
    requests_per_minute = 100
    tokens_per_minute   = 100000
    requests_per_day    = 10000
  }
  
  soft_budget_limit = true  # Allow exceeding budget with warnings
  
  tags = {
    team        = "engineering"
    environment = "production"
    purpose     = "api-gateway"
  }
}
```

### Auto-Renewing Key with Notifications

```hcl
resource "litellm_api_key_enhanced" "auto_renew_key" {
  key_alias = "long-term-service"
  user_id   = "automation-user"
  
  permissions {
    models = ["gpt-3.5-turbo"]
    max_budget = 50.0
    budget_duration = "1mo"
  }
  
  expires_at          = timeadd(timestamp(), "365d")  # 1 year
  auto_renew          = true
  renewal_period_days = 30  # Renew 30 days before expiry
  
  notification_config {
    budget_alerts = [50, 80, 100]  # Alert at 50%, 80%, and 100% of budget
    expiry_alert_days = 14         # Alert 14 days before expiry
    webhook_url = "https://api.company.com/litellm/notifications"
  }
}
```

### Restricted API Key with Metadata Filters

```hcl
resource "litellm_api_key_enhanced" "restricted_key" {
  key_alias = "customer-api-key"
  user_id   = "customer-123"
  
  permissions {
    models = ["gpt-3.5-turbo"]
    max_budget = 10.0
    budget_duration = "1d"
    
    # Only allow specific endpoints
    allowed_endpoints = [
      "/v1/chat/completions"
    ]
    
    # Metadata filters for additional validation
    metadata_filters = {
      customer_id = "customer-123"
      project_id  = "project-456"
      max_tokens  = "1000"
    }
  }
  
  rate_limits {
    requests_per_minute = 10
    tokens_per_minute   = 10000
  }
  
  expires_at = timeadd(timestamp(), "30d")
}
```

### Environment-Specific Keys

```hcl
variable "environments" {
  default = {
    dev = {
      budget = 10
      rpm    = 100
      models = ["gpt-3.5-turbo"]
    }
    staging = {
      budget = 50
      rpm    = 500
      models = ["gpt-3.5-turbo", "gpt-4"]
    }
    prod = {
      budget = 200
      rpm    = 1000
      models = ["gpt-3.5-turbo", "gpt-4", "gpt-4-turbo"]
    }
  }
}

resource "litellm_api_key_enhanced" "env_keys" {
  for_each = var.environments
  
  key_alias = "${each.key}-api-key"
  
  permissions {
    models          = each.value.models
    max_budget      = each.value.budget
    budget_duration = "1mo"
  }
  
  rate_limits {
    requests_per_minute = each.value.rpm
  }
  
  tags = {
    environment = each.key
    managed_by  = "terraform"
  }
}
```

## Argument Reference

### Required Arguments

- `key_alias` (String) - Alias for the API key. Used for identification and management.

### Optional Arguments

- `user_id` (String) - User ID associated with this key.
- `team_id` (String) - Team ID associated with this key.
- `expires_at` (String) - Expiration time for the key in RFC3339 format (e.g., "2024-12-31T23:59:59Z").
- `soft_budget_limit` (Boolean) - If true, allows requests to exceed budget with warnings. Default: `false`.
- `auto_renew` (Boolean) - Automatically renew key before expiration. Default: `false`.
- `renewal_period_days` (Number) - Days before expiration to auto-renew (1-30). Default: `7`.
- `tags` (Map of String) - Tags for organizing and filtering keys.

#### permissions Block

- `permissions` (Block List, Max: 1) - Permissions and restrictions for the API key.
  - `models` (Set of String) - List of allowed models.
  - `max_budget` (Number) - Maximum budget for this key (0 = unlimited). Default: `0`.
  - `budget_duration` (String) - Budget duration. Valid values: `"1d"`, `"1w"`, `"1mo"`, `"1y"`. Default: `"1mo"`.
  - `max_parallel_requests` (Number) - Maximum parallel requests allowed (1-10000). Default: `100`.
  - `allowed_endpoints` (Set of String) - List of allowed API endpoints.
  - `metadata_filters` (Map of String) - Metadata filters for request validation.

#### notification_config Block

- `notification_config` (Block List, Max: 1) - Notification configuration for key events.
  - `budget_alerts` (Set of Number) - Budget thresholds for alerts (percentages, 1-100).
  - `expiry_alert_days` (Number) - Days before expiry to send alert (1-30). Default: `7`.
  - `webhook_url` (String) - Webhook URL for notifications.

#### rate_limits Block

- `rate_limits` (Block List, Max: 1) - Rate limiting configuration.
  - `requests_per_minute` (Number) - Requests per minute limit (1-100000).
  - `tokens_per_minute` (Number) - Tokens per minute limit (1-10000000).
  - `requests_per_day` (Number) - Requests per day limit (1-10000000).

## Attributes Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier of the API key (same as key_hash).
- `key` (String, Sensitive) - The actual API key. Only available immediately after creation.
- `key_hash` (String) - Hash of the API key for identification.
- `created_at` (String) - Creation timestamp.
- `last_used_at` (String) - Last usage timestamp.
- `total_spend` (Number) - Total spend on this key.
- `import_hash` (String) - Key hash for importing existing keys (used internally).

## Import

API keys can be imported using their key hash:

```bash
terraform import litellm_api_key_enhanced.example sk_1234567890abcdef
```

Note: The actual key value cannot be retrieved after creation. Importing only allows management of the key's configuration.

## Use Cases

### 1. Customer API Key Management

```hcl
variable "customers" {
  type = map(object({
    tier   = string
    budget = number
  }))
}

locals {
  tier_configs = {
    free = {
      models = ["gpt-3.5-turbo"]
      rpm    = 10
      tpm    = 10000
    }
    starter = {
      models = ["gpt-3.5-turbo", "gpt-4"]
      rpm    = 100
      tpm    = 100000
    }
    enterprise = {
      models = ["gpt-3.5-turbo", "gpt-4", "gpt-4-turbo"]
      rpm    = 1000
      tpm    = 1000000
    }
  }
}

resource "litellm_api_key_enhanced" "customer_keys" {
  for_each = var.customers
  
  key_alias = "customer-${each.key}"
  user_id   = each.key
  
  permissions {
    models          = local.tier_configs[each.value.tier].models
    max_budget      = each.value.budget
    budget_duration = "1mo"
    
    metadata_filters = {
      customer_id   = each.key
      customer_tier = each.value.tier
    }
  }
  
  rate_limits {
    requests_per_minute = local.tier_configs[each.value.tier].rpm
    tokens_per_minute   = local.tier_configs[each.value.tier].tpm
  }
  
  notification_config {
    budget_alerts = [80, 100]
    webhook_url   = "https://api.company.com/customer-notifications/${each.key}"
  }
  
  expires_at = timeadd(timestamp(), "365d")
  auto_renew = each.value.tier == "enterprise"
  
  tags = {
    customer_id = each.key
    tier        = each.value.tier
  }
}
```

### 2. Microservice Authentication

```hcl
variable "microservices" {
  default = {
    "user-service" = {
      endpoints = ["/v1/chat/completions"]
      models    = ["gpt-3.5-turbo"]
      budget    = 50
    }
    "analytics-service" = {
      endpoints = ["/v1/embeddings"]
      models    = ["text-embedding-ada-002"]
      budget    = 100
    }
    "recommendation-service" = {
      endpoints = ["/v1/chat/completions", "/v1/embeddings"]
      models    = ["gpt-4", "text-embedding-ada-002"]
      budget    = 200
    }
  }
}

resource "litellm_api_key_enhanced" "microservice_keys" {
  for_each = var.microservices
  
  key_alias = each.key
  
  permissions {
    models            = each.value.models
    allowed_endpoints = each.value.endpoints
    max_budget        = each.value.budget
    budget_duration   = "1mo"
    
    metadata_filters = {
      service_name = each.key
      environment  = terraform.workspace
    }
  }
  
  rate_limits {
    requests_per_minute = 1000
  }
  
  soft_budget_limit = terraform.workspace != "prod"
  
  tags = {
    service     = each.key
    environment = terraform.workspace
    managed_by  = "terraform"
  }
}
```

### 3. Time-Limited Access Keys

```hcl
resource "litellm_api_key_enhanced" "contractor_key" {
  key_alias = "contractor-${var.contractor_id}"
  user_id   = var.contractor_id
  
  permissions {
    models     = ["gpt-3.5-turbo"]
    max_budget = 25.0
    budget_duration = "1w"
  }
  
  # Expires at end of contract
  expires_at = var.contract_end_date
  
  # No auto-renewal for contractors
  auto_renew = false
  
  notification_config {
    expiry_alert_days = 7
    webhook_url       = var.hr_notification_webhook
  }
  
  tags = {
    type         = "contractor"
    contractor_id = var.contractor_id
    department   = var.department
  }
}

# Create budget alert for contractor spending
resource "litellm_budget_alert" "contractor_spending" {
  alert_name        = "Contractor ${var.contractor_id} Budget Alert"
  threshold_percent = 90.0
  budget_scope      = "key"
  scope_id          = litellm_api_key_enhanced.contractor_key.key_hash
  
  notification_channels = [
    "email:manager@company.com"
  ]
}
```

### 4. API Key Rotation

```hcl
variable "rotation_enabled" {
  type    = bool
  default = true
}

# Primary key
resource "litellm_api_key_enhanced" "primary" {
  key_alias = "service-primary"
  
  permissions {
    models     = var.allowed_models
    max_budget = var.monthly_budget
  }
  
  expires_at = timeadd(timestamp(), "90d")
  auto_renew = true
  
  tags = {
    rotation_group = "service-keys"
    key_type       = "primary"
  }
}

# Secondary key for rotation
resource "litellm_api_key_enhanced" "secondary" {
  count = var.rotation_enabled ? 1 : 0
  
  key_alias = "service-secondary"
  
  permissions {
    models     = var.allowed_models
    max_budget = var.monthly_budget
  }
  
  expires_at = timeadd(timestamp(), "180d")
  auto_renew = true
  
  tags = {
    rotation_group = "service-keys"
    key_type       = "secondary"
  }
}

# Output both keys for application configuration
output "api_keys" {
  value = {
    primary   = litellm_api_key_enhanced.primary.key
    secondary = var.rotation_enabled ? litellm_api_key_enhanced.secondary[0].key : null
  }
  sensitive = true
}
```

## Notes

- The actual API key is only available in the `key` attribute immediately after creation. Store it securely.
- API keys are hashed before storage - the original key cannot be retrieved after creation.
- Imported keys can be managed but their actual key value is not accessible.
- Budget limits reset according to the `budget_duration` setting.
- Rate limits are enforced in real-time and return 429 status codes when exceeded.
- `metadata_filters` are matched against request metadata for additional validation.
- Auto-renewal creates a new key and deprecates the old one - update your applications accordingly.
- Tags are useful for cost allocation, compliance tracking, and key organization.
- Notification webhooks should return 2xx status codes; failures are retried with exponential backoff.