# LiteLLM Terraform Provider

This Terraform provider allows you to manage LiteLLM resources through Infrastructure as Code. It provides support for managing models, teams, team members, and API keys via the LiteLLM REST API.

## Features

- Manage LiteLLM model configurations
- Associate models with specific teams
- Create and manage teams
- Configure team members and their permissions
- Set usage limits and budgets
- Control access to specific models
- Specify model modes (e.g., completion, embedding, image generation)
- Manage API keys with fine-grained controls
- Support for reasoning effort configuration in the model resource

## Compatibility

- [Terraform](https://developer.hashicorp.com/terraform/install) >= 1.0.0 (provider protocol 6.0)
- [OpenTofu](https://opentofu.org/docs/intro/install/) >= 1.6.0
- [Go](https://go.dev/doc/install) >= 1.24.0 for provider development
- Tested backend: exactly LiteLLM 1.98.0

The provider's global client baseline remains Terraform 1.0.0 or OpenTofu 1.6.0. The optional write-only attributes `litellm_key.key_wo`, `litellm_key.send_invite_email`, and `litellm_user.send_invite_email` require Terraform or OpenTofu 1.11.0 or later only when they are configured.

## Using the Provider

To use the LiteLLM provider in your Terraform configuration, you need to declare it in the <code>terraform</code> block:

```hcl
terraform {
  required_version = ">= 1.0.0"

  required_providers {
    litellm = {
      source  = "registry.terraform.io/ncecere/litellm"
      version = ">= 2.0.1, < 3.0.0"
    }
  }
}

provider "litellm" {
  api_base = var.litellm_api_base
  api_key  = var.litellm_api_key
}
```

Run `terraform init -upgrade` (or `tofu init -upgrade`) after changing the version constraint. The published source is exactly `registry.terraform.io/ncecere/litellm`.

Correcting the provider binary's served address to that published source does not change protocol 6, provider or resource schemas, HCL types, state values, IDs, or import formats. Normal state created with the published `ncecere/litellm` source needs no migration. If development-only configuration actually recorded state under the unpublished `registry.terraform.io/nicholas-cecere/litellm` address, migrate it explicitly; the provider does not silently alias addresses:

```sh
terraform state replace-provider \
  registry.terraform.io/nicholas-cecere/litellm \
  registry.terraform.io/ncecere/litellm
# OpenTofu users can run the equivalent `tofu state replace-provider` command.
```

Then, you can use the provider to manage LiteLLM resources. Here's an example of creating a model configuration:

```hcl
resource "litellm_model" "gpt4" {
  model_name          = "gpt-4-proxy"
  custom_llm_provider = "openai"
  model_api_key       = var.openai_api_key
  model_api_base      = "https://api.openai.com/v1"
  base_model          = "gpt-4"
  tier                = "paid"
  mode                = "chat"
  reasoning_effort    = "medium"  # Optional: "low", "medium", or "high"
  
  input_cost_per_million_tokens  = 30.0
  output_cost_per_million_tokens = 60.0
}
```

For full details on the <code>litellm_model</code> resource, see the [model resource documentation](docs/resources/model.md).

Here's an example of creating an API key with various options:

```hcl
resource "litellm_key" "example_key" {
  models               = ["gpt-4", "claude-3.5-sonnet"]
  allowed_routes       = ["/chat/completions", "/keys/*"]
  max_budget           = 100.0
  user_id              = "user123"
  team_id              = "team456"
  max_parallel_requests = 5
  tpm_limit            = 1000
  rpm_limit            = 60
  budget_duration      = "monthly"
  key_alias            = "prod-key-1"
  duration             = "30d"
  metadata             = {
    environment = "production"
  }
  allowed_cache_controls = ["no-cache", "max-age=3600"]
  soft_budget          = 80.0
  aliases              = {
    "gpt-4" = "gpt4"
  }
  config               = {
    default_model = "gpt-4"
  }
  permissions          = {
    can_create_keys = "true"
  }
  model_max_budget     = {
    "gpt-4" = 50.0
  }
  model_rpm_limit      = {
    "claude-3.5-sonnet" = 30
  }
  model_tpm_limit      = {
    "gpt-4" = 500
  }
  guardrails           = ["content_filter", "token_limit"]
  blocked              = false
  tags                 = ["production", "api"]
}
```

The <code>litellm_key</code> resource supports the following options:

- <code>models</code>: List of allowed models for this key
- <code>allowed_routes</code> / <code>allowed_passthrough_routes</code>: Restrict which LiteLLM proxy routes this key may call
- <code>max_budget</code>: Maximum budget for the key
- <code>user_id</code> and <code>team_id</code>: Associate the key with a user and team
- <code>service_account_id</code>: Create a team-owned service account key (defaults <code>key_alias</code> and injects metadata)
- <code>max_parallel_requests</code>: Limit concurrent requests
- <code>tpm_limit</code> and <code>rpm_limit</code>: Set tokens and requests per minute limits
- <code>budget_duration</code>: Specify budget duration (e.g., "monthly", "weekly")
- <code>key_alias</code>: Set a friendly name for the key
- <code>duration</code>: Set the key's validity period
- <code>metadata</code>: Add custom metadata to the key
- <code>allowed_cache_controls</code>: Specify allowed cache control directives
- <code>soft_budget</code>: Set a soft budget limit
- <code>aliases</code>: Define model aliases
- <code>config</code>: Set configuration options
- <code>permissions</code>: Specify key permissions
- <code>model_max_budget</code>, <code>model_rpm_limit</code>, <code>model_tpm_limit</code>: Set per-model limits
- <code>guardrails</code>: Apply specific guardrails to the key
- <code>blocked</code>: Flag to block/unblock the key
- <code>tags</code>: Add tags for organization and filtering

When <code>team_id</code> is set and <code>models</code> is omitted, the provider automatically allows the key to use all models in that team by sending <code>["all-team-models"]</code> to the API.

For full details on the <code>litellm_key</code> resource, see the [key resource documentation](docs/resources/key.md).

### Available Resources

- <code>litellm_model</code>: Manage model configurations. [Documentation](docs/resources/model.md)
- <code>litellm_key</code>: Manage API keys. [Documentation](docs/resources/key.md)
- <code>litellm_key_block</code>: Block/unblock an API key. [Documentation](docs/resources/key_block.md)
- <code>litellm_team</code>: Manage teams. [Documentation](docs/resources/team.md)
- <code>litellm_team_block</code>: Block/unblock a team. [Documentation](docs/resources/team_block.md)
- <code>litellm_team_member</code>: Manage an individual team member. [Documentation](docs/resources/team_member.md)
- <code>litellm_team_member_add</code>: Manage a batch of team members. [Documentation](docs/resources/team_member_add.md)
- <code>litellm_organization</code>: Manage organizations. [Documentation](docs/resources/organization.md)
- <code>litellm_organization_member</code>: Manage organization members. [Documentation](docs/resources/organization_member.md)
- <code>litellm_project</code>: Manage projects (between teams and keys in the hierarchy). [Documentation](docs/resources/project.md)
- <code>litellm_user</code>: Manage users. [Documentation](docs/resources/user.md)
- <code>litellm_budget</code>: Manage reusable budgets and rate limits. [Documentation](docs/resources/budget.md)
- <code>litellm_tag</code>: Manage tags for categorizing resources. [Documentation](docs/resources/tag.md)
- <code>litellm_access_group</code>: Manage model access groups. [Documentation](docs/resources/access_group.md)
- <code>litellm_unified_access_group</code>: Manage access groups via the `/v1/access_group` API. [Documentation](docs/resources/unified_access_group.md)
- <code>litellm_fallback</code>: Manage model fallback configuration. [Documentation](docs/resources/fallback.md)
- <code>litellm_guardrail</code>: Manage content-safety guardrails. [Documentation](docs/resources/guardrail.md)
- <code>litellm_prompt</code>: Manage reusable prompt templates. [Documentation](docs/resources/prompt.md)
- <code>litellm_agent</code>: Manage LiteLLM Agents (A2A). [Documentation](docs/resources/agent.md)
- <code>litellm_search_tool</code>: Manage web-search tool configurations. [Documentation](docs/resources/search_tool.md)
- <code>litellm_mcp_server</code>: Manage MCP (Model Context Protocol) servers. [Documentation](docs/resources/mcp_server.md)
- <code>litellm_credential</code>: Manage credentials for secure authentication. [Documentation](docs/resources/credential.md)
- <code>litellm_vector_store</code>: Manage vector stores for embeddings and RAG. [Documentation](docs/resources/vector_store.md)
- <code>litellm_jwt_key_mapping</code>: Manage JWT string claim-to-existing-virtual-key mappings. [Documentation](docs/resources/jwt_key_mapping.md)

### Available Data Sources

Single-item lookups:

- <code>litellm_model</code> ([docs](docs/data-sources/model.md)), <code>litellm_key</code> ([docs](docs/data-sources/key.md)), <code>litellm_team</code> ([docs](docs/data-sources/team.md)), <code>litellm_organization</code> ([docs](docs/data-sources/organization.md)), <code>litellm_project</code> ([docs](docs/data-sources/project.md)), <code>litellm_user</code> ([docs](docs/data-sources/user.md)), <code>litellm_budget</code> ([docs](docs/data-sources/budget.md)), <code>litellm_tag</code> ([docs](docs/data-sources/tag.md)), <code>litellm_access_group</code> ([docs](docs/data-sources/access_group.md)), <code>litellm_unified_access_group</code> ([docs](docs/data-sources/unified_access_group.md)), <code>litellm_prompt</code> ([docs](docs/data-sources/prompt.md)), <code>litellm_guardrail</code> ([docs](docs/data-sources/guardrail.md)), <code>litellm_agent</code> ([docs](docs/data-sources/agent.md)), <code>litellm_search_tool</code> ([docs](docs/data-sources/search_tool.md)), <code>litellm_fallback</code> ([docs](docs/data-sources/fallback.md)), <code>litellm_mcp_server</code> ([docs](docs/data-sources/mcp_server.md)), <code>litellm_credential</code> ([docs](docs/data-sources/credential.md)), <code>litellm_vector_store</code> ([docs](docs/data-sources/vector_store.md)), <code>litellm_jwt_key_mapping</code> ([docs](docs/data-sources/jwt_key_mapping.md))

List data sources (return all items of a type):

- <code>litellm_models</code> ([docs](docs/data-sources/models.md)), <code>litellm_keys</code> ([docs](docs/data-sources/keys.md)), <code>litellm_teams</code> ([docs](docs/data-sources/teams.md)), <code>litellm_organizations</code> ([docs](docs/data-sources/organizations.md)), <code>litellm_projects</code> ([docs](docs/data-sources/projects.md)), <code>litellm_users</code> ([docs](docs/data-sources/users.md)), <code>litellm_budgets</code> ([docs](docs/data-sources/budgets.md)), <code>litellm_tags</code> ([docs](docs/data-sources/tags.md)), <code>litellm_access_groups</code> ([docs](docs/data-sources/access_groups.md)), <code>litellm_unified_access_groups</code> ([docs](docs/data-sources/unified_access_groups.md)), <code>litellm_prompts</code> ([docs](docs/data-sources/prompts.md)), <code>litellm_guardrails</code> ([docs](docs/data-sources/guardrails.md)), <code>litellm_agents</code> ([docs](docs/data-sources/agents.md)), <code>litellm_search_tools</code> ([docs](docs/data-sources/search_tools.md)), <code>litellm_mcp_servers</code> ([docs](docs/data-sources/mcp_servers.md)), <code>litellm_jwt_key_mappings</code> ([docs](docs/data-sources/jwt_key_mappings.md))

## Development

### Project Structure

The project is organized as follows:

```
terraform-provider-litellm/
├── internal/
│   └── provider/               # the provider implementation (registered in main.go)
│       ├── provider.go         # provider schema + resource/data-source registration
│       ├── client.go           # LiteLLM API HTTP client
│       ├── resource_*.go       # resource implementations and helpers
│       ├── datasource_*.go     # data-source implementations and helpers
│       └── *_test.go           # unit tests
├── docs/                       # resource & data-source documentation
├── internal_testing/           # docker compose LiteLLM + smoke-test configs
├── main.go                     # provider entrypoint
├── go.mod
├── go.sum
├── Makefile
└── ...
```

### Implementation and compatibility

This repository publishes a Terraform provider binary and its HCL interface; it
is not a supported external Go library API. Provider implementation packages are
internal details. The binary entry point in `main.go` serves only the Terraform
Plugin Framework implementation in `internal/provider`.

The former top-level `litellm/` Terraform Plugin SDKv2 implementation was left
behind by the Framework migration and was never registered by the migrated
provider binary. Removing that dead implementation does not require a state
migration: the provider type name, registered resource and data-source type
names, schema versions and types, state values and IDs, and import formats are
unchanged. Provider source identity and the one development-only migration case
are documented in [Using the Provider](#using-the-provider).

The checked LiteLLM OpenAPI, hidden-route supplement, provenance manifest, and
offline Go verifier protect the provider's HTTP API boundary without adding any
runtime Python or source dependency. See [API contract maintenance](docs/development/api-contract.md)
for reproduction, upgrade review, and FastAPI/Pydantic limitations.

### Building the Provider

1. Clone the repository:
```sh
git clone https://github.com/ncecere/terraform-provider-litellm.git
```

2. Enter the repository directory:
```sh
cd terraform-provider-litellm
```

3. Build and install the provider:
```sh
make install
```

### Development Commands

The Makefile provides several useful commands for development:

- `make build`: Builds the provider
- `make install`: Builds and installs the provider
- `make test`: Runs the test suite
- `make fmt`: Formats the code
- `make vet`: Runs go vet
- `make contract-check`: Verifies the checked LiteLLM API contract offline
- `make contract-update`: Regenerates the contract from the exact pinned upstream source
- `make contract-diff`: Reproduces and compares the pinned contract without modifying files
- `make lint`: Runs golangci-lint
- `make clean`: Removes build artifacts and installed provider

### Testing

**Unit tests**

```sh
make test
```

**Smoke tests** (manual run against a local LiteLLM proxy) exercise real plan/apply/destroy for selected resources and data sources. Results are written to `internal_testing/.smoke/smoke.log`.

1. Start the proxy and DB: `make local` (runs `docker compose up -d` in `internal_testing/`).
2. Optionally follow logs: `make logs`.
3. Run smoke for one or more files (at least one of `resources=` or `datasources=` required, comma-separated):

   ```sh
   make smoke resources=model_minimal.tf
   make smoke datasources=keys_list.tf
   make smoke resources=model_minimal.tf,key_minimal.tf datasources=keys_list.tf,model.tf
   ```

   All listed files are applied in a single Terraform run (shared state). Requires `make build` and a valid `internal_testing/terraform.tfvars` (copy from `terraform.tfvars.example`).

4. Inspect output: `internal_testing/.smoke/smoke.log` (no-color, section headers PLAN / APPLY / DESTROY / SUMMARY).

See [internal_testing/README.md](internal_testing/README.md) for full details (Docker layout, directory structure, tfvars).

### Contributing

Contributions are welcome! Please read our [contributing guidelines](CONTRIBUTING.md) first.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Notes

- Always use environment variables or secure secret management solutions to handle sensitive information like API keys and AWS credentials.
- Refer to the comprehensive documentation in the `docs/` directory for detailed usage examples and configuration options.
- Make sure to keep your provider version updated for the latest features and bug fixes.
- The provider now supports AWS cross-account access with `aws_session_name` and `aws_role_name` parameters in the model resource.
- All example configurations have been consolidated into the documentation for better organization and maintenance.
