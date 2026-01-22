# LiteLLM Provider

The LiteLLM provider allows Terraform to manage LiteLLM resources. LiteLLM is a proxy service that standardizes the input/output across different LLM APIs, providing a unified interface for various language model providers.

## Example Usage

```hcl
terraform {
  required_providers {
    litellm = {
      source = "registry.terraform.io/ncecere/litellm"
    }
  }
}

provider "litellm" {
  api_base = "https://your-litellm-proxy.com"
  api_key  = var.litellm_api_key
}

# Create a credential for storing API keys securely
resource "litellm_credential" "openai" {
  credential_name = "openai-production"
  credential_values = {
    "api_key" = var.openai_api_key
  }
}

# Basic model configuration using credential
resource "litellm_model" "gpt4" {
  model_name              = "gpt-4-proxy"
  custom_llm_provider     = "openai"
  base_model              = "gpt-4"
  tier                    = "paid"
  mode                    = "chat"
  litellm_credential_name = litellm_credential.openai.credential_name
  
  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}

# Organization and team hierarchy
resource "litellm_organization" "company" {
  organization_alias = "my-company"
  max_budget         = 5000.0
}

resource "litellm_team" "dev_team" {
  team_alias      = "development-team"
  organization_id = litellm_organization.company.organization_id
  models          = [litellm_model.gpt4.model_name]
  max_budget      = 1000.0
}

# Create an API key for the team
resource "litellm_key" "dev_key" {
  key_alias = "dev-api-key"
  team_id   = litellm_team.dev_team.team_id
}
```

## Provider Arguments

The following arguments are supported in the provider block:

* `api_base` - (Required) The base URL of your LiteLLM instance. Can also be set via the `LITELLM_API_BASE` environment variable.
* `api_key` - (Required) The API key for authenticating with LiteLLM. Can also be set via the `LITELLM_API_KEY` environment variable.
* `insecure_skip_verify` - (Optional) Skip TLS certificate verification. Defaults to `false`.
* `litellm_changed_by` - (Optional) Value for the litellm-changed-by header to track actions performed by authorized users.
* `additional_headers` - (Optional) Additional HTTP headers to set on requests to the LiteLLM API.

## Authentication

The LiteLLM provider requires an API key and base URL for authentication. These can be provided in the provider configuration block or via environment variables.

### Environment Variables

```bash
export LITELLM_API_BASE="https://your-litellm-proxy.com"
export LITELLM_API_KEY="your-api-key"
```

```hcl
# Provider will automatically use environment variables
provider "litellm" {}
```

## Available Resources

The LiteLLM provider supports the following resources:

### Core Resources

* [`litellm_model`](./resources/model.md) - Manage LiteLLM model configurations
* [`litellm_key`](./resources/key.md) - Manage API keys
* [`litellm_key_block`](./resources/key_block.md) - Block/unblock API keys
* [`litellm_credential`](./resources/credential.md) - Manage credentials for various providers

### Organization & Team Management

* [`litellm_organization`](./resources/organization.md) - Manage organizations
* [`litellm_organization_member`](./resources/organization_member.md) - Manage organization memberships
* [`litellm_team`](./resources/team.md) - Manage teams and their permissions
* [`litellm_team_block`](./resources/team_block.md) - Block/unblock teams
* [`litellm_team_member`](./resources/team_member.md) - Manage team member configurations
* [`litellm_team_member_add`](./resources/team_member_add.md) - Add members to teams
* [`litellm_user`](./resources/user.md) - Manage users

### Budget & Access Control

* [`litellm_budget`](./resources/budget.md) - Manage budget configurations
* [`litellm_access_group`](./resources/access_group.md) - Manage model access groups
* [`litellm_tag`](./resources/tag.md) - Manage tags for organization

### AI Safety & Configuration

* [`litellm_prompt`](./resources/prompt.md) - Manage prompt templates
* [`litellm_guardrail`](./resources/guardrail.md) - Manage content guardrails

### Integrations

* [`litellm_mcp_server`](./resources/mcp_server.md) - Manage MCP (Model Context Protocol) servers
* [`litellm_search_tool`](./resources/search_tool.md) - Manage search tool configurations
* [`litellm_vector_store`](./resources/vector_store.md) - Manage vector stores

## Available Data Sources

### Single Resource Lookups

* [`litellm_model`](./data-sources/model.md) - Retrieve model information
* [`litellm_key`](./data-sources/key.md) - Retrieve API key information
* [`litellm_team`](./data-sources/team.md) - Retrieve team information
* [`litellm_organization`](./data-sources/organization.md) - Retrieve organization information
* [`litellm_user`](./data-sources/user.md) - Retrieve user information
* [`litellm_credential`](./data-sources/credential.md) - Retrieve credential information
* [`litellm_budget`](./data-sources/budget.md) - Retrieve budget information
* [`litellm_tag`](./data-sources/tag.md) - Retrieve tag information
* [`litellm_access_group`](./data-sources/access_group.md) - Retrieve access group information
* [`litellm_prompt`](./data-sources/prompt.md) - Retrieve prompt information
* [`litellm_guardrail`](./data-sources/guardrail.md) - Retrieve guardrail information
* [`litellm_mcp_server`](./data-sources/mcp_server.md) - Retrieve MCP server information
* [`litellm_search_tool`](./data-sources/search_tool.md) - Retrieve search tool information
* [`litellm_vector_store`](./data-sources/vector_store.md) - Retrieve vector store information

### List Data Sources

* [`litellm_models`](./data-sources/models.md) - List all models
* [`litellm_keys`](./data-sources/keys.md) - List all API keys
* [`litellm_teams`](./data-sources/teams.md) - List all teams
* [`litellm_organizations`](./data-sources/organizations.md) - List all organizations
* [`litellm_users`](./data-sources/users.md) - List all users
* [`litellm_budgets`](./data-sources/budgets.md) - List all budgets
* [`litellm_tags`](./data-sources/tags.md) - List all tags
* [`litellm_access_groups`](./data-sources/access_groups.md) - List all access groups
* [`litellm_prompts`](./data-sources/prompts.md) - List all prompts
* [`litellm_guardrails`](./data-sources/guardrails.md) - List all guardrails
* [`litellm_mcp_servers`](./data-sources/mcp_servers.md) - List all MCP servers
* [`litellm_search_tools`](./data-sources/search_tools.md) - List all search tools

## Examples

The provider includes comprehensive examples in the `examples/` directory:

| Example | Description |
|---------|-------------|
| [minimal](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/minimal) | Simplest possible setup |
| [complete](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/complete) | Full enterprise configuration |
| [multi-provider](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/multi-provider) | Multiple LLM providers |
| [data-sources](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/data-sources) | Using data sources |
| [mcp-servers](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/mcp-servers) | MCP server configurations |
| [search-tools](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/search-tools) | Search tool configurations |

## Getting Started

1. Install the provider by adding it to your Terraform configuration
2. Configure your LiteLLM instance URL and API key
3. Start with the [minimal example](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/minimal) to create basic resources
4. Explore the [complete example](https://github.com/ncecere/terraform-provider-litellm/tree/main/examples/complete) for enterprise configurations

## Best Practices

1. **Use credentials** - Store API keys in `litellm_credential` resources instead of directly in model configurations
2. **Organize with hierarchy** - Use organizations → teams → users for proper access control
3. **Set budget limits** - Always configure `max_budget` on teams and keys
4. **Use access groups** - Simplify model access management with access groups
5. **Configure guardrails** - Protect against harmful content with guardrails
6. **Tag resources** - Use tags for cost allocation and filtering
