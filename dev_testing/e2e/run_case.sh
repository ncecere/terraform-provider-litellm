#!/usr/bin/env bash
set -euo pipefail

case_name=${1:?case name required}
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
case_dir="$repo_root/dev_testing/e2e/$case_name"
base_tfvars="$repo_root/dev_testing/dev.auto.tfvars"
provider_bin="$repo_root/terraform-provider-litellm"
plugin_dir="$HOME/.terraform.d/plugins/registry.terraform.io/local/litellm/1.0.0/darwin_arm64"

mkdir -p "$plugin_dir"
(cd "$repo_root" && go build -o terraform-provider-litellm)
cp "$provider_bin" "$plugin_dir/terraform-provider-litellm_v1.0.0"

rm -rf "$case_dir"
mkdir -p "$case_dir"
cp "$base_tfvars" "$case_dir/dev.auto.tfvars"
cat > "$case_dir/main.tf" <<'TF'
terraform {
  required_providers {
    litellm = {
      source  = "local/litellm"
      version = "1.0.0"
    }
  }
}

variable "litellm_api_base" { type = string }
variable "litellm_api_key" {
  type      = string
  sensitive = true
}
variable "suffix" { type = string }

provider "litellm" {
  api_base = var.litellm_api_base
  api_key  = var.litellm_api_key
}
TF

suffix="$(date +%s)-$RANDOM"
case "$case_name" in
  team)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-team-${var.suffix}"
}

data "litellm_team" "test" {
  team_id = litellm_team.test.id
}

output "id" { value = litellm_team.test.id }
TF
    ;;
  key)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_key" "test" {
  key_alias = "tf-e2e-key-${var.suffix}"
}

data "litellm_key" "test" {
  key = litellm_key.test.key
}

output "id" { value = litellm_key.test.id }
TF
    ;;
  user)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_user" "test" {
  user_email      = "tf-e2e-user-${var.suffix}@example.com"
  user_alias      = "tf-e2e-user-${var.suffix}"
  auto_create_key = false
}

data "litellm_user" "test" {
  user_id = litellm_user.test.user_id
}

output "id" { value = litellm_user.test.user_id }
TF
    ;;
  budget)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_budget" "test" {
  budget_id   = "tf-e2e-budget-${var.suffix}"
  max_budget  = 10
  budget_duration = "30d"
}

data "litellm_budget" "test" {
  budget_id = litellm_budget.test.id
}

output "id" { value = litellm_budget.test.id }
TF
    ;;

  model_resource_only)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_model" "test" {
  model_name          = "tf-e2e-model-only-${var.suffix}"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

output "id" { value = litellm_model.test.id }
TF
    ;;
  model)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_model" "test" {
  model_name          = "tf-e2e-model-${var.suffix}"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

data "litellm_model" "test" {
  model_id = litellm_model.test.id
}

output "id" { value = litellm_model.test.id }
TF
    ;;
  project_key)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-project-team-${var.suffix}"
  models     = ["gpt-4o-mini"]
}

resource "litellm_project" "test" {
  project_alias = "tf-e2e-project-${var.suffix}"
  team_id       = litellm_team.test.id
  models        = ["gpt-4o-mini"]
}

resource "litellm_key" "test" {
  key_alias  = "tf-e2e-project-key-${var.suffix}"
  team_id    = litellm_team.test.id
  project_id = litellm_project.test.id
  models     = ["gpt-4o-mini"]
}

output "project_id" { value = litellm_project.test.id }
output "key_id" { value = litellm_key.test.id }
TF
    ;;
  access_group)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_access_group" "test" {
  access_group = "tf-e2e-access-group-${var.suffix}"
  model_names  = ["gpt-4o-mini"]
}

data "litellm_access_group" "test" {
  access_group = litellm_access_group.test.access_group
}

output "id" { value = litellm_access_group.test.id }
TF
    ;;
  unified_access_group)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-uag-team-${var.suffix}"
}

resource "litellm_unified_access_group" "test" {
  access_group_name  = "tf-e2e-uag-${var.suffix}"
  description        = "Terraform E2E unified access group"
  access_model_names = ["gpt-4o-mini"]
  assigned_team_ids  = [litellm_team.test.id]
}

data "litellm_unified_access_group" "test" {
  access_group_id = litellm_unified_access_group.test.id
}

data "litellm_unified_access_groups" "all" {
  depends_on = [litellm_unified_access_group.test]
}

output "id" { value = litellm_unified_access_group.test.id }
output "name" { value = data.litellm_unified_access_group.test.access_group_name }
TF
    ;;
  tag)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_tag" "test" {
  name = "tf-e2e-tag-${var.suffix}"
}

data "litellm_tag" "test" {
  name = litellm_tag.test.name
}

output "id" { value = litellm_tag.test.id }
TF
    ;;
  mcp_server)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_mcp_server" "test" {
  server_name   = "tf_e2e_mcp_${replace(var.suffix, "-", "_")}"
  alias         = "tf_e2e_mcp_${replace(var.suffix, "-", "_")}"
  url           = "https://example.com/mcp"
  transport     = "http"
  auth_type     = "none"
  extra_headers = ["X-Test-Header"]
}

output "id" { value = litellm_mcp_server.test.id }
TF
    ;;



  list_datasources)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-list-team-${var.suffix}"
}

resource "litellm_user" "test" {
  user_email      = "tf-e2e-list-user-${var.suffix}@example.com"
  auto_create_key = false
}

resource "litellm_key" "test" {
  key_alias = "tf-e2e-list-key-${var.suffix}"
}


resource "litellm_budget" "test" {
  budget_id       = "tf-e2e-list-budget-${var.suffix}"
  max_budget      = 10
  budget_duration = "30d"
}

resource "litellm_organization" "test" {
  organization_alias = "tf-e2e-list-org-${var.suffix}"
}

resource "litellm_project" "test" {
  project_alias = "tf-e2e-list-project-${var.suffix}"
  team_id       = litellm_team.test.id
}

resource "litellm_access_group" "test" {
  access_group = "tf-e2e-list-access-${var.suffix}"
  model_names  = ["gpt-4o-mini"]
}

resource "litellm_guardrail" "test" {
  guardrail_name = "tf-e2e-list-guardrail-${var.suffix}"
  guardrail      = "aporia"
  mode           = "pre_call"
}

resource "litellm_search_tool" "test" {
  search_tool_name = "tf-e2e-list-search-${var.suffix}"
  search_provider  = "tavily"
  api_key          = "tvly-fake-key"
}

resource "litellm_tag" "test" {
  name = "tf-e2e-list-tag-${var.suffix}"
}

resource "litellm_mcp_server" "test" {
  server_name = "tf_e2e_list_mcp_${replace(var.suffix, "-", "_")}"
  url         = "https://example.com/mcp"
  transport   = "http"
  auth_type   = "none"
}

resource "litellm_agent" "test" {
  agent_name = "tf-e2e-list-agent-${var.suffix}"
  agent_card {
    name = "tf-e2e-list-agent-${var.suffix}"
    url  = "https://example.com/agent"
  }
}

data "litellm_teams" "all" { depends_on = [litellm_team.test] }
data "litellm_users" "all" { depends_on = [litellm_user.test] }
data "litellm_keys" "all" { depends_on = [litellm_key.test] }
data "litellm_models" "all" {}
data "litellm_budgets" "all" { depends_on = [litellm_budget.test] }
data "litellm_organizations" "all" { depends_on = [litellm_organization.test] }
data "litellm_projects" "all" { depends_on = [litellm_project.test] }
data "litellm_access_groups" "all" { depends_on = [litellm_access_group.test] }
data "litellm_guardrails" "all" { depends_on = [litellm_guardrail.test] }
data "litellm_search_tools" "all" { depends_on = [litellm_search_tool.test] }
data "litellm_tags" "all" { depends_on = [litellm_tag.test] }
data "litellm_mcp_servers" "all" { depends_on = [litellm_mcp_server.test] }
data "litellm_agents" "all" { depends_on = [litellm_agent.test] }
data "litellm_prompts" "all" {}

TF
    ;;
  agent)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_agent" "test" {
  agent_name = "tf-e2e-agent-${var.suffix}"
  agent_card {
    name = "tf-e2e-agent-${var.suffix}"
    url  = "https://example.com/agent"
  }
}

data "litellm_agent" "test" {
  id = litellm_agent.test.id
}

output "id" { value = litellm_agent.test.id }
TF
    ;;
  organization)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_organization" "test" {
  organization_alias = "tf-e2e-org-${var.suffix}"
  models             = ["gpt-4o-mini"]
}

data "litellm_organization" "test" {
  organization_id = litellm_organization.test.id
}

output "id" { value = litellm_organization.test.id }
TF
    ;;
  prompt)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_prompt" "test" {
  prompt_id          = "tf-e2e-prompt-${var.suffix}"
  prompt_integration = "dotprompt"
  dotprompt_content  = "Hello {{name}}"
}

data "litellm_prompt" "test" {
  prompt_id = litellm_prompt.test.id
}

output "id" { value = litellm_prompt.test.id }
TF
    ;;
  guardrail)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_guardrail" "test" {
  guardrail_name = "tf-e2e-guardrail-${var.suffix}"
  guardrail      = "aporia"
  mode           = "pre_call"
}

data "litellm_guardrail" "test" {
  guardrail_id = litellm_guardrail.test.id
}

output "id" { value = litellm_guardrail.test.id }
TF
    ;;
  search_tool)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_search_tool" "test" {
  search_tool_name = "tf-e2e-search-${var.suffix}"
  search_provider  = "tavily"
  api_key          = "tvly-fake-key"
}

data "litellm_search_tool" "test" {
  search_tool_id = litellm_search_tool.test.id
}

output "id" { value = litellm_search_tool.test.id }
TF
    ;;
  credential)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_credential" "test" {
  credential_name = "tf-e2e-cred-${var.suffix}"
  credential_info = {
    description = "Terraform E2E credential"
  }
  credential_values = {
    api_key = "sk-fake-e2e-key"
  }
}

data "litellm_credential" "test" {
  credential_name = litellm_credential.test.credential_name
}

output "id" { value = litellm_credential.test.id }
TF
    ;;
  vector_store)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_vector_store" "test" {
  vector_store_name        = "tf-e2e-vs-${var.suffix}"
  custom_llm_provider      = "openai"
  vector_store_description = "Terraform E2E vector store"
}

data "litellm_vector_store" "test" {
  vector_store_id = litellm_vector_store.test.id
}

output "id" { value = litellm_vector_store.test.id }
TF
    ;;
  fallback)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_model" "primary" {
  model_name          = "tf-e2e-fallback-primary-${var.suffix}"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_model" "fallback" {
  model_name          = "tf-e2e-fallback-secondary-${var.suffix}"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_fallback" "test" {
  model           = litellm_model.primary.model_name
  fallback_models = [litellm_model.fallback.model_name]
}

output "id" { value = litellm_fallback.test.id }
TF
    ;;
  team_member)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-team-member-team-${var.suffix}"
}

resource "litellm_user" "test" {
  user_email      = "tf-e2e-team-member-${var.suffix}@example.com"
  auto_create_key = false
}

resource "litellm_team_member" "test" {
  team_id    = litellm_team.test.id
  user_id    = litellm_user.test.user_id
  user_email = litellm_user.test.user_email
  role       = "user"
}

output "id" { value = litellm_team_member.test.id }
TF
    ;;
  team_member_add)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-team-member-add-team-${var.suffix}"
}

resource "litellm_team_member_add" "test" {
  team_id = litellm_team.test.id
  member {
    user_email = "tf-e2e-team-member-add-${var.suffix}@example.com"
    role       = "user"
  }
}

output "id" { value = litellm_team_member_add.test.id }
TF
    ;;
  team_block)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_team" "test" {
  team_alias = "tf-e2e-team-block-${var.suffix}"
}

resource "litellm_team_block" "test" {
  team_id = litellm_team.test.id
}

output "id" { value = litellm_team_block.test.id }
TF
    ;;
  key_block)
    cat > "$case_dir/case.tf" <<'TF'
resource "litellm_key" "test" {
  key_alias = "tf-e2e-key-block-${var.suffix}"
}

resource "litellm_key_block" "test" {
  key = litellm_key.test.key
}

output "id" { value = litellm_key_block.test.id }
TF
    ;;
  *) echo "unknown case: $case_name" >&2; exit 2 ;;
esac

cd "$case_dir"
terraform init -input=false >/dev/null
terraform apply -auto-approve -input=false -var="suffix=$suffix"
terraform plan -input=false -detailed-exitcode -var="suffix=$suffix" >/tmp/tf-plan-$case_name.log || code=$?
code=${code:-0}
if [ "$code" = "2" ]; then
  echo "Plan after apply has changes:" >&2
  cat /tmp/tf-plan-$case_name.log >&2
  terraform destroy -auto-approve -input=false -var="suffix=$suffix" || true
  exit 1
elif [ "$code" != "0" ]; then
  cat /tmp/tf-plan-$case_name.log >&2
  terraform destroy -auto-approve -input=false -var="suffix=$suffix" || true
  exit "$code"
fi
terraform destroy -auto-approve -input=false -var="suffix=$suffix"
