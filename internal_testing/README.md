# Internal Testing

Manual verification configs for every resource and data source in the provider.
Includes a Docker Compose stack to spin up a local LiteLLM proxy and uses
`dev_overrides` to point Terraform at the locally-built binary -- no
`terraform init` required.

## Quick Start

```bash
# From the repo root
cd /Users/nickcecere/Projects/Terraform/terraform-provider-litellm

# 1. Start the LiteLLM proxy + Postgres
cd internal_testing
docker compose up -d
docker compose logs -f litellm          # wait for "LiteLLM Proxy is running"
# Ctrl-C once it's healthy

# 2. Build the provider binary (back in repo root)
cd ..
go build -o terraform-provider-litellm

# 3. Set TF_CLI_CONFIG_FILE to use the dev_overrides terraformrc
export TF_CLI_CONFIG_FILE="$(pwd)/internal_testing/terraformrc"

# 4. Create your tfvars (one-time -- defaults match docker-compose)
cp internal_testing/terraform.tfvars.example internal_testing/terraform.tfvars

# 5. Copy in whichever resources you want to test and run
cd internal_testing
cp resources/model_minimal.tf .
terraform plan
terraform apply
```

## Docker Compose

The `docker-compose.yml` starts two services:

| Service   | Image                                        | Port  |
|-----------|----------------------------------------------|-------|
| `litellm` | `docker.litellm.ai/berriai/litellm:main-stable` | 4000  |
| `db`      | `postgres:16`                                | 5432  |

**Defaults** (no `.env` file needed):
- API base: `http://localhost:4000`
- Master key: `sk-testing-key`
- Models stored in DB (`STORE_MODEL_IN_DB=True`), so the Terraform provider
  can create them at runtime.

```bash
# Start
docker compose up -d

# View logs
docker compose logs -f litellm

# Stop (keep data)
docker compose down

# Stop and wipe all data
docker compose down -v
```

## Directory Layout

```
internal_testing/
  docker-compose.yml           # LiteLLM + Postgres stack
  litellm-config.yaml          # minimal LiteLLM proxy config
  terraformrc                  # dev_overrides pointing at local binary
  provider.tf                  # provider + required_providers block
  variables.tf                 # api_base and api_key variables
  terraform.tfvars.example     # copy to terraform.tfvars (defaults work)

  resources/                   # one file per resource, minimal + full variants
    model_minimal.tf
    model_full.tf
    key_minimal.tf
    key_full.tf
    key_block_minimal.tf       # blocks the minimal key (destructive)
    team_minimal.tf
    team_full.tf
    team_block_minimal.tf      # blocks the minimal team (destructive)
    team_member_minimal.tf
    team_member_full.tf
    team_member_add_minimal.tf
    team_member_add_full.tf
    user_minimal.tf
    user_full.tf
    organization_minimal.tf
    organization_full.tf
    organization_member_minimal.tf
    organization_member_full.tf
    budget_minimal.tf
    budget_full.tf
    credential_minimal.tf
    credential_full.tf
    tag_minimal.tf
    tag_full.tf
    access_group_minimal.tf
    access_group_full.tf
    prompt_minimal.tf
    prompt_full.tf
    guardrail_minimal.tf
    guardrail_full.tf
    mcp_server_minimal.tf
    mcp_server_full.tf
    vector_store_minimal.tf
    vector_store_full.tf
    search_tool_minimal.tf
    search_tool_full.tf

  datasources/                 # one file per data source
    model.tf
    key.tf
    team.tf
    organization.tf
    user.tf
    budget.tf
    credential_minimal.tf
    credential_full.tf
    tag.tf
    access_group.tf
    prompt.tf
    guardrail.tf
    mcp_server.tf
    vector_store.tf
    search_tool.tf
    models_list.tf
    keys_list.tf
    teams_list.tf
    organizations_list.tf
    users_list.tf
    budgets_list.tf
    tags_list.tf
    access_groups_list.tf
    prompts_list.tf
    guardrails_list.tf
    mcp_servers_list.tf
    search_tools_list.tf
```

## Testing a Subset

Since Terraform loads all `.tf` files in a directory, the `resources/` and
`datasources/` directories are separate from the root. To test only specific
resources, copy the files you want into the root:

```bash
cd internal_testing

# Test just a single resource
cp resources/model_minimal.tf .
terraform plan
terraform apply
rm model_minimal.tf

# Test a resource + its data source
cp resources/team_minimal.tf .
cp datasources/team.tf .
terraform plan
terraform apply
rm team_minimal.tf team.tf
```

Or, run directly against a subdirectory (resources only, no data sources):

```bash
# Copy provider files in temporarily
cp provider.tf variables.tf terraform.tfvars resources/
cd resources
terraform plan
terraform apply

# Clean up
rm provider.tf variables.tf terraform.tfvars
```

## Notes

- The `key_block` and `team_block` resources are **destructive** -- they
  block the referenced key/team. Don't include them unless you intend to
  test blocking behavior.
- `organization_member` and `team_member` resources depend on their parent
  organization/team existing first. The files reference the minimal/full
  resource instances via `litellm_organization.minimal.id` etc.
- Data sources reference resources from the `resources/` files. To use them,
  both the resource file and data source file must be in the same working
  directory.
- Provider credentials can also be set via environment variables instead of
  tfvars:
  ```bash
  export LITELLM_API_BASE="http://localhost:4000"
  export LITELLM_API_KEY="sk-testing-key"
  ```
- To wipe all state and start fresh:
  ```bash
  cd internal_testing
  rm -f terraform.tfstate terraform.tfstate.backup
  rm -f *.tf.bak
  docker compose down -v   # wipes the database too
  docker compose up -d     # fresh start
  ```
