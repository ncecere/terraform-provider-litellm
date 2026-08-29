#!/bin/sh
# Isolated smoke test: plan -> apply -> no-drift plan -> destroy.
# Usage: smoke.sh <repo_root> resources <file...> datasources <file...>

set -eu

REPO_ROOT=${1:?usage: smoke.sh <repo_root> resources <file...> datasources <file...>}
shift
REPO_ROOT=$(cd "$REPO_ROOT" && pwd -P)
INTERNAL_TESTING="$REPO_ROOT/internal_testing"
RESOURCES="$INTERNAL_TESTING/resources"
DATASOURCES="$INTERNAL_TESTING/datasources"
PROVIDER_DIR=${PROVIDER_DIR:-$REPO_ROOT}
SMOKE_ASSEMBLY_ONLY=${SMOKE_ASSEMBLY_ONLY:-0}
SMOKE_PRIVATE_ROOT=${SMOKE_PRIVATE_ROOT:-$INTERNAL_TESTING}
SMOKE_DELETE_LOGS=${SMOKE_DELETE_LOGS:-0}
SMOKE_DIAGNOSTIC_OUTPUT=${SMOKE_DIAGNOSTIC_OUTPUT:-}

if [ "$SMOKE_ASSEMBLY_ONLY" != "1" ] && [ ! -f "$PROVIDER_DIR/terraform-provider-litellm" ]; then
  echo "Provider binary not found at $PROVIDER_DIR/terraform-provider-litellm; run 'make build'." >&2
  exit 1
fi
if ! command -v terraform >/dev/null 2>&1; then
  echo "terraform is required for smoke tests." >&2
  exit 1
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to encode the dev_overrides path safely." >&2
  exit 1
fi

mkdir -p "$SMOKE_PRIVATE_ROOT/.smoke-logs"
chmod 700 "$SMOKE_PRIVATE_ROOT" "$SMOKE_PRIVATE_ROOT/.smoke-logs"
SMOKE_DIR=$(mktemp -d "$SMOKE_PRIVATE_ROOT/.smoke.XXXXXX")
SMOKE_LOG=${SMOKE_LOG_OVERRIDE:-$SMOKE_PRIVATE_ROOT/.smoke-logs/$(date '+%Y%m%d-%H%M%S')-$$.log}
case "$SMOKE_LOG" in "$SMOKE_PRIVATE_ROOT"/.smoke-logs/*.log) ;; *) echo 'Unsafe smoke log override.' >&2; exit 1 ;; esac
[ ! -e "$SMOKE_LOG" ] || { echo 'Smoke log destination already exists.' >&2; exit 1; }
if [ -n "$SMOKE_DIAGNOSTIC_OUTPUT" ]; then
  case "$SMOKE_DIAGNOSTIC_OUTPUT" in "$SMOKE_PRIVATE_ROOT"/.smoke-logs/*.command.log) ;; *) echo 'Unsafe smoke diagnostic destination.' >&2; exit 1 ;; esac
  [ ! -e "$SMOKE_DIAGNOSTIC_OUTPUT" ] || { echo 'Smoke diagnostic destination already exists.' >&2; exit 1; }
fi
: >"$SMOKE_LOG"
chmod 600 "$SMOKE_LOG"
APPLY_STARTED=0
SUCCESS=0
CLEANUP_ARGS=
IMPORT_BACKUP=

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  cleanup_status=0
  if [ "$SUCCESS" -eq 1 ]; then
    rm -rf "$SMOKE_DIR"
    [ "$SMOKE_DELETE_LOGS" = 1 ] && rm -f "$SMOKE_LOG"
    exit 0
  fi

  if [ -n "$IMPORT_BACKUP" ] && [ -f "$IMPORT_BACKUP" ]; then
    # A failed import after state rm must not orphan the seeded remote object.
    cp "$IMPORT_BACKUP" "$SMOKE_DIR/terraform.tfstate" || cleanup_status=1
  fi
  if [ "$APPLY_STARTED" -eq 1 ] && [ -f "$SMOKE_DIR/terraform.tfstate" ]; then
    echo "Attempting bounded cleanup after failure..." >&3
    cleanup_attempt=1
    while [ "$cleanup_attempt" -le 2 ]; do
      # shellcheck disable=SC2086 # CLEANUP_ARGS is one optional complete argument.
      if (cd "$SMOKE_DIR" && terraform destroy -refresh=false -auto-approve $CLEANUP_ARGS) >>"$SMOKE_LOG" 2>&1; then
        if [ -z "$(cd "$SMOKE_DIR" && terraform state list 2>>"$SMOKE_LOG")" ]; then
          cleanup_status=0
          break
        fi
      fi
      cleanup_status=1
      cleanup_attempt=$((cleanup_attempt + 1))
    done
  fi
  # Cleanup failure overrides an otherwise successful nested command and the
  # sole state/log recovery material is retained. Never claim hosted teardown
  # as authoritative remote absence.
  if [ "$cleanup_status" -ne 0 ]; then status=1; fi
  echo "Smoke failed; private workspace and recovery log retained" >&3
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

trim() { printf '%s\n' "$1" | sed 's/^[,[:space:]]*//;s/[,[:space:]]*$//'; }

validate_fixture_name() {
  case "$1" in
    ''|.*|*..*|*[!A-Za-z0-9_.-]*) return 1 ;;
    *.tf) return 0 ;;
    *) return 1 ;;
  esac
}

expand_arg() {
  _arg=$1
  while [ -n "$_arg" ]; do
    _f=${_arg%%,*}
    _rest=${_arg#*,}
    [ "$_rest" = "$_arg" ] && _rest=
    _arg=$_rest
    _f=$(trim "$_f")
    [ -n "$_f" ] && printf '%s\n' "$_f"
  done
}

exec 3>&1
exec >>"$SMOKE_LOG" 2>&1
cp "$INTERNAL_TESTING/provider.tf" "$INTERNAL_TESTING/variables.tf" "$SMOKE_DIR/"
cp "$INTERNAL_TESTING/terraform.tfvars.example" "$SMOKE_DIR/terraform.tfvars"

provider_dir_hcl=$(python3 -c 'import json, sys; print(json.dumps(sys.argv[1]))' "$PROVIDER_DIR")
cat >"$SMOKE_DIR/terraformrc" <<EOF
provider_installation {
  dev_overrides {
    "registry.terraform.io/ncecere/litellm" = $provider_dir_hcl
    "registry.opentofu.org/ncecere/litellm"  = $provider_dir_hcl
  }
  direct {}
}
EOF
# Do not let ambient Terraform flags or provider variables redirect this run.
# In particular, acceptance preflights localhost and must not be overridable by
# TF_CLI_ARGS_plan=-var=litellm_api_base=... or TF_VAR_litellm_api_base.
unset TF_CLI_ARGS TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_CLI_ARGS_destroy
unset TF_VAR_litellm_api_base TF_VAR_litellm_api_key
unset LITELLM_API_BASE LITELLM_API_KEY
export TF_CLI_CONFIG_FILE="$SMOKE_DIR/terraformrc"
export TF_CLI_ARGS=-no-color
export TF_IN_AUTOMATION=1

RESOURCE_NAMES=
DATASOURCE_NAMES=
DIR=
FOUND=0
MISSING=0
while [ "$#" -gt 0 ]; do
  case "$1" in
    resources) DIR=$RESOURCES; shift ;;
    datasources) DIR=$DATASOURCES; shift ;;
    *)
      for file in $(expand_arg "$1"); do
        validate_fixture_name "$file" || {
          echo "Unsafe smoke fixture name rejected: $file" >&3
          exit 1
        }
        if [ -n "$DIR" ] && [ -f "$DIR/$file" ]; then
          name=$(basename "$file")
          if [ "$DIR" = "$RESOURCES" ]; then
            assembled_name="resource_$name"
          else
            assembled_name="datasource_$name"
          fi
          if [ -e "$SMOKE_DIR/$assembled_name" ]; then
            echo "Duplicate smoke fixture: $assembled_name" >&3
            exit 1
          fi
          cp "$DIR/$file" "$SMOKE_DIR/$assembled_name"
          FOUND=1
          if [ "$DIR" = "$RESOURCES" ]; then
            RESOURCE_NAMES="$RESOURCE_NAMES $name"
          else
            DATASOURCE_NAMES="$DATASOURCE_NAMES $name"
          fi
        else
          if [ -n "$DIR" ]; then
            echo "Requested smoke file not found: $file" >&3
            MISSING=1
          fi
        fi
      done
      shift
      ;;
  esac
done

if [ "$FOUND" -ne 1 ]; then
  echo "No requested files were found under internal_testing." >&3
  exit 1
fi
if [ "$MISSING" -ne 0 ]; then
  echo "Refusing a partial smoke run because one or more requested files are missing." >&3
  exit 1
fi

printf '\n========== Isolated smoke test ==========\n'
[ -n "$RESOURCE_NAMES" ] && echo "Resources:$RESOURCE_NAMES"
[ -n "$DATASOURCE_NAMES" ] && echo "Datasources:$DATASOURCE_NAMES"

cd "$SMOKE_DIR"
if [ "$SMOKE_ASSEMBLY_ONLY" = "1" ]; then
  echo '=== ASSEMBLY FORMAT AND VALIDATION CHECK ==='
  terraform fmt -check -diff .
  terraform validate
  SUCCESS=1
  printf '\nSmoke assembly passed: fixture names are collision-free and all assembled HCL validates and is formatted.\n' >&3
  echo "Results written to $SMOKE_LOG" >&3
  exit 0
fi

echo '=== PLAN ==='
terraform plan -out=tfplan
terraform show -json tfplan >matrix-initial-plan.json

echo '=== APPLY ==='
APPLY_STARTED=1
set +e
terraform apply -auto-approve tfplan >initial-apply.log 2>&1
initial_apply_status=$?
set -e
cat initial-apply.log
if [ "$initial_apply_status" -ne 0 ]; then
  if [ -n "$SMOKE_DIAGNOSTIC_OUTPUT" ]; then
    python3 - initial-apply.log "$SMOKE_DIAGNOSTIC_OUTPUT" <<'PY'
import os,sys
source,destination=sys.argv[1:]
raw=open(source,"rb").read()
flags=os.O_WRONLY|os.O_CREAT|os.O_EXCL|getattr(os,"O_NOFOLLOW",0)
fd=os.open(destination,flags,0o600)
try:
    os.write(fd,raw); os.fsync(fd)
finally:
    os.close(fd)
PY
  fi
  exit "$initial_apply_status"
fi

if [ -f datasource_agent_structured_parity.tf ]; then
  echo '=== AGENT SINGLE/LIST STRUCTURED PARITY ==='
  terraform output -json ds_agent_structured_projection >agent-single-projection.json
  terraform output -json ds_agents_structured_parity >agent-list-projection.json
  python3 - agent-single-projection.json agent-list-projection.json <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    single = json.load(stream, parse_int=int)
with open(sys.argv[2], encoding="utf-8") as stream:
    listed = json.load(stream, parse_int=int)
if single != listed:
    raise SystemExit("Single-agent and list-agent structured projections differ.")
PY
fi

if [ -f resource_guardrail_minimal.tf ]; then
  echo '=== GUARDRAIL SINGLE/LIST PRESENCE ==='
  terraform output -raw guardrail_minimal_id >guardrail-managed-id.txt
  terraform output -json guardrail_registry_ids >guardrail-registry-ids.json
  python3 - guardrail-managed-id.txt guardrail-registry-ids.json <<'PY'
import json, sys
managed = open(sys.argv[1], encoding="utf-8").read()
with open(sys.argv[2], encoding="utf-8") as stream:
    registry = json.load(stream)
if managed not in registry:
    raise SystemExit("The v2 guardrail registry omitted the Terraform-managed guardrail.")
PY
fi

if [ -f resource_prompt_minimal.tf ]; then
  echo '=== PROMPT SINGLE/LIST PRESENCE ==='
  terraform output -raw prompt_minimal_id >prompt-managed-id.txt
  terraform output -json prompt_development_registry_ids >prompt-registry-ids.json
  python3 - prompt-managed-id.txt prompt-registry-ids.json <<'PY'
import json, sys
managed = open(sys.argv[1], encoding="utf-8").read()
with open(sys.argv[2], encoding="utf-8") as stream:
    registry = json.load(stream)
if managed not in registry:
    raise SystemExit("The environment-scoped prompt inventory omitted the managed prompt.")
PY
fi

STEADY_ARGS=
if [ "${SMOKE_CREDENTIAL_IMPORT:-}" = "1" ]; then
  echo '=== CREDENTIAL SOURCE-FREE IMPORT ==='
  IMPORT_BACKUP="$SMOKE_DIR/credential-import-seed.tfstate"
  cp terraform.tfstate "$IMPORT_BACKUP"
  terraform state rm 'litellm_credential.seed[0]'
  terraform import \
    -var=credential_import_phase=imported \
    'litellm_credential.imported[0]' \
    'test/cred%import-雪'
  rm -f "$IMPORT_BACKUP"
  IMPORT_BACKUP=
  STEADY_ARGS='-var=credential_import_phase=imported'
  CLEANUP_ARGS=$STEADY_ARGS
elif [ "${SMOKE_FALLBACK_IMPORT:-}" = "1" ]; then
  echo '=== FALLBACK SPECIAL-IDENTITY IMPORT ==='
  IMPORT_BACKUP="$SMOKE_DIR/fallback-import-seed.tfstate"
  cp terraform.tfstate "$IMPORT_BACKUP"
  terraform state rm 'litellm_fallback.fallback_import_seed[0]'
  terraform import \
    -var=fallback_import_phase=imported \
    'litellm_fallback.fallback_imported[0]' \
    'smoke-fallback:8b?variant=50%-雪:general'
  rm -f "$IMPORT_BACKUP"
  IMPORT_BACKUP=
  STEADY_ARGS='-var=fallback_import_phase=imported'
  CLEANUP_ARGS=$STEADY_ARGS
elif [ "${SMOKE_MCP_CLEAR_LIFECYCLE:-}" = "1" ]; then
  echo '=== MCP PRESENCE-AWARE SET TO CLEAR ==='
  terraform apply -auto-approve -var=mcp_clear_phase=cleared
  STEADY_ARGS='-var=mcp_clear_phase=cleared'
  CLEANUP_ARGS=$STEADY_ARGS
  mcp_clear_id=$(terraform output -raw mcp_clear_lifecycle_id)
  curl --fail --silent --show-error -H 'Authorization: Bearer sk-testing-key' "http://localhost:4000/v1/mcp/server/$mcp_clear_id" >mcp-cleared.json
  python3 - mcp-cleared.json <<'PY'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8"))
# Credentials and credentials.scopes are intentionally excluded: v1.98
# management reads redact them, so unit/protocol tests verify their exact wire intent.
for field in ("alias","description","command","authorization_url","token_url","registration_url","oauth2_flow","instructions","byok_api_key_help_url","source_url","timeout","max_concurrent_requests"):
    assert value.get(field) is None,(field,value.get(field))
for field in ("mcp_access_groups","args","allowed_tools","extra_headers","byok_description","env_vars"):
    assert value.get(field)==[],(field,value.get(field))
for field in ("env","static_headers","tool_name_to_display_name","tool_name_to_description"):
    assert value.get(field)=={},(field,value.get(field))
for field in ("allow_all_keys","delegate_auth_to_upstream","oauth_passthrough","is_byok"):
    assert value.get(field) is False,(field,value.get(field))
assert value.get("dcr_bridge") is None,value.get("dcr_bridge")
assert value.get("available_on_public_internet") is True,value.get("available_on_public_internet")
PY
  for refresh_number in 1 2; do
    echo "=== MCP CLEAR REFRESH-ONLY $refresh_number ==="
    terraform apply -refresh-only -auto-approve $STEADY_ARGS
  done
  set +e
  terraform plan -detailed-exitcode $STEADY_ARGS >mcp-clear-no-drift.log 2>&1
  mcp_clear_plan_status=$?
  set -e
  cat mcp-clear-no-drift.log
  [ "$mcp_clear_plan_status" -eq 0 ] || { echo 'MCP clear did not reach zero drift.' >&3; exit 1; }
  echo '=== MCP CLEAR IMPORT AND REPEATED REFRESH ==='
  terraform state rm 'litellm_mcp_server.clear_lifecycle'
  terraform import $STEADY_ARGS 'litellm_mcp_server.clear_lifecycle' "$mcp_clear_id"
  for refresh_number in 1 2; do terraform apply -refresh-only -auto-approve $STEADY_ARGS; done
  set +e
  terraform plan -detailed-exitcode $STEADY_ARGS >mcp-clear-import-no-drift.log 2>&1
  mcp_clear_import_status=$?
  set -e
  cat mcp-clear-import-no-drift.log
  [ "$mcp_clear_import_status" -eq 0 ] || { echo 'Imported MCP clear did not remain at zero drift.' >&3; exit 1; }
  terraform destroy -auto-approve $STEADY_ARGS
  terraform state list >matrix-final-state.list
  [ ! -s matrix-final-state.list ] || { echo 'MCP clear cleanup left state.' >&3; exit 1; }
  SUCCESS=1
  printf '\nSmoke passed: MCP set-to-clear, exact observable sentinels, no drift, import, repeated refresh, and cleanup succeeded.\n' >&3
  exit 0
elif [ "${SMOKE_MCP_TOKEN_EXCHANGE_LIFECYCLE:-}" = "1" ]; then
  echo '=== MCP TOKEN EXCHANGE SET TO CLEAR ==='
  terraform apply -auto-approve -var=mcp_token_exchange_phase=cleared
  STEADY_ARGS='-var=mcp_token_exchange_phase=cleared'
  CLEANUP_ARGS=$STEADY_ARGS
  mcp_token_exchange_id=$(terraform output -raw mcp_token_exchange_lifecycle_id)
  curl --fail --silent --show-error -H 'Authorization: Bearer sk-testing-key' "http://localhost:4000/v1/mcp/server/$mcp_token_exchange_id" >mcp-token-exchange-cleared.json
  python3 - mcp-token-exchange-cleared.json <<'PY'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8"))
for field in ("issuer","token_exchange_endpoint","audience","subject_token_type","token_exchange_profile"):
    assert value.get(field) is None,(field,value.get(field))
PY
  for refresh_number in 1 2; do
    echo "=== MCP TOKEN EXCHANGE CLEAR REFRESH-ONLY $refresh_number ==="
    terraform apply -refresh-only -auto-approve $STEADY_ARGS
  done
  set +e
  terraform plan -detailed-exitcode $STEADY_ARGS >mcp-token-exchange-no-drift.log 2>&1
  mcp_token_exchange_plan_status=$?
  set -e
  cat mcp-token-exchange-no-drift.log
  [ "$mcp_token_exchange_plan_status" -eq 0 ] || { echo 'MCP token-exchange clear did not reach zero drift.' >&3; exit 1; }
  terraform destroy -auto-approve $STEADY_ARGS
  terraform state list >matrix-final-state.list
  [ ! -s matrix-final-state.list ] || { echo 'MCP token-exchange cleanup left state.' >&3; exit 1; }
  SUCCESS=1
  printf '\nSmoke passed: MCP token-exchange set-to-clear, repeated refresh, no drift, and cleanup succeeded.\n' >&3
  exit 0
elif [ "${SMOKE_MCP_IMPORT:-}" = "1" ]; then
  echo '=== MCP IMPORT PROJECTION ==='
  mcp_import_id=$(terraform output -raw mcp_import_seed_id)
  IMPORT_BACKUP="$SMOKE_DIR/mcp-import-seed.tfstate"
  cp terraform.tfstate "$IMPORT_BACKUP"
  terraform state rm 'litellm_mcp_server.mcp_import_seed[0]'
  terraform import \
    -var=mcp_import_phase=imported \
    'litellm_mcp_server.mcp_imported[0]' \
    "$mcp_import_id"
  STEADY_ARGS='-var=mcp_import_phase=imported'
  CLEANUP_ARGS=$STEADY_ARGS
  terraform state pull >mcp-import-private-before.json
  for refresh_number in 1 2; do
    echo "=== MCP IMPORT REFRESH-ONLY APPLY $refresh_number ==="
    set +e
    # Persist provider-private provenance from each authoritative refresh; a
    # plan-only refresh discards the updated private state before stability is
    # checked by the next process.
    # shellcheck disable=SC2086 # STEADY_ARGS is one complete optional argument.
    terraform apply -refresh-only -auto-approve $STEADY_ARGS >"mcp-import-refresh-$refresh_number.log" 2>&1
    refresh_status=$?
    set -e
    cat "mcp-import-refresh-$refresh_number.log"
    if [ "$refresh_status" -ne 0 ]; then
      echo "MCP import refresh-only apply $refresh_number was not immediately stable (exit $refresh_status)." >&3
      exit 1
    fi
  done
  terraform state pull >mcp-import-private-after.json
  python3 - mcp-import-private-before.json mcp-import-private-after.json <<'PY'
import hashlib,hmac,json,secrets,sys
key=secrets.token_bytes(32)
def private(path):
    value=json.load(open(path,encoding="utf-8"))
    rows=[]
    for resource in value.get("resources",[]):
        if resource.get("type") != "litellm_mcp_server": continue
        for instance in resource.get("instances",[]): rows.append(instance.get("private") or "")
    if len(rows)!=1 or not rows[0]: raise SystemExit(1)
    return hmac.new(key,rows[0].encode(),hashlib.sha256).digest()
if not hmac.compare_digest(private(sys.argv[1]),private(sys.argv[2])): raise SystemExit(1)
PY
  echo '=== MCP IMPORT CONFIG OWNERSHIP CONVERGENCE APPLY ==='
  # shellcheck disable=SC2086 # STEADY_ARGS is one complete optional argument.
  terraform apply -auto-approve $STEADY_ARGS
  if [ -n "${SMOKE_MCP_EVIDENCE:-}" ]; then
    python3 - "$SMOKE_MCP_EVIDENCE" <<'PY'
import json,sys
with open(sys.argv[1], "x", encoding="utf-8") as output:
    json.dump({"immediate_import":True,"refresh_only_zero_drift_count":2,"private_provenance_preserved":True}, output, sort_keys=True)
    output.write("\n")
PY
    chmod 600 "$SMOKE_MCP_EVIDENCE"
  fi
  rm -f "$IMPORT_BACKUP"
  IMPORT_BACKUP=
  echo '=== MCP IMPORT CLEANUP ==='
  terraform destroy -auto-approve $STEADY_ARGS
  terraform state list >matrix-final-state.list
  [ ! -s matrix-final-state.list ] || { echo 'MCP import cleanup left state.' >&3; exit 1; }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || { echo 'MCP smoke log exceeded its private bound.' >&3; exit 1; }
  SUCCESS=1
  printf '\nSmoke passed: MCP immediate import, two zero-drift refresh plans, provenance persistence, and cleanup succeeded.\n' >&3
  exit 0
elif [ "${SMOKE_CREDENTIAL_UPDATE:-}" = "1" ]; then
  echo '=== CREDENTIAL UPDATE APPLY ==='
  terraform apply -auto-approve -var=credential_update_phase=after
  STEADY_ARGS='-var=credential_update_phase=after'
  CLEANUP_ARGS=$STEADY_ARGS
elif [ "${SMOKE_AGENT_LIFECYCLE:-}" = "1" ]; then
  echo '=== AGENT SET-TO-CLEAR APPLY ==='
  terraform apply -auto-approve -var=agent_lifecycle_phase=cleared
  agent_lifecycle_id=$(terraform output -raw agent_lifecycle_id)
  echo '=== AGENT DIRECT API CLEAR VERIFICATION ==='
  curl --fail --silent --show-error -H 'Authorization: Bearer sk-testing-key' "http://localhost:4000/v1/agents/$agent_lifecycle_id" >agent-cleared.json
  python3 - agent-cleared.json <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
for field in ("tpm_limit", "rpm_limit", "session_tpm_limit", "session_rpm_limit"):
    assert value.get(field) is None, (field, value.get(field))
assert "qualifier" not in value.get("litellm_params", {})
assert value.get("static_headers") in ({}, None)
assert value.get("extra_headers") in ([], None)
permission = value.get("object_permission") or {}
for field in ("mcp_servers", "mcp_access_groups", "models", "agents"):
    assert permission.get(field) in ([], None), (field, permission.get(field))
assert permission.get("mcp_tool_permissions") in ({}, None)
card = value.get("agent_card_params") or {}
for field in ("description", "preferredTransport", "iconUrl", "documentationUrl"):
    assert field not in card or card[field] is None, (field, card.get(field))
provider = card.get("provider") or {}
assert provider.get("organization") is None
skills = {skill["id"]: skill for skill in card.get("skills", [])}
skill = skills["acceptance"]
for field in ("tags", "examples", "inputModes", "outputModes"):
    assert skill.get(field) in ([], None), (field, skill.get(field))
PY
  echo '=== AGENT DIRECT API-OWNED NULL/OMISSION/EXACT-NUMBER SEED ==='
  curl --fail --silent --show-error -H 'Authorization: Bearer sk-testing-key' "http://localhost:4000/v1/agents/$agent_lifecycle_id" >agent-before-adversarial.json
  python3 - agent-before-adversarial.json agent-adversarial-patch.json <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as stream:
    value = json.load(stream)
card = value["agent_card_params"]
card["signatures"] = [
    {"protected": "api-null", "signature": "api", "header": None},
    {"protected": "api-omitted", "signature": "api"},
]
skills = [skill for skill in card.get("skills", []) if skill.get("id") not in {"api-null", "api-omitted"}]
skills.extend([
    {"id": "api-null", "name": "API Null", "security": None},
    {"id": "api-omitted", "name": "API Omitted"},
])
card["skills"] = skills
patch = {
    "litellm_params": {
        "model": "openai/gpt-4o-mini",
        "api_owned_large": 9007199254740993,
        "api_owned_null": None,
        "api_owned_nested": {"present": None, "exact": 9007199254740993},
    },
    "static_headers": {"X-API-Owned": "preserve"},
    "agent_card_params": card,
}
with open(sys.argv[2], "w", encoding="utf-8") as stream:
    json.dump(patch, stream, separators=(",", ":"))
PY
  curl --fail --silent --show-error -X PATCH \
    -H 'Authorization: Bearer sk-testing-key' -H 'Content-Type: application/json' \
    --data-binary @agent-adversarial-patch.json \
    "http://localhost:4000/v1/agents/$agent_lifecycle_id" >/dev/null
  echo '=== AGENT CLEARED IMPORT/OWNERSHIP APPLY ==='
  terraform state rm litellm_agent.lifecycle
  terraform import -var=agent_lifecycle_phase=cleared litellm_agent.lifecycle "$agent_lifecycle_id"
  terraform apply -auto-approve -var=agent_lifecycle_phase=cleared
  echo '=== AGENT UNRELATED LEGACY/CARD UPDATE OVER AUTHORITATIVE BASE ==='
  terraform apply -auto-approve -var=agent_lifecycle_phase=adversarial
  curl --fail --silent --show-error -H 'Authorization: Bearer sk-testing-key' "http://localhost:4000/v1/agents/$agent_lifecycle_id" >agent-imported.json
  python3 - agent-imported.json <<'PY'
import json, sys
value = json.load(open(sys.argv[1], encoding="utf-8"), parse_int=int)
params = value.get("litellm_params", {})
assert params.get("model") == "openai/gpt-4o-mini-adversarial"
assert params.get("api_owned_large") == 9007199254740993
assert "api_owned_null" in params and params["api_owned_null"] is None
assert params.get("api_owned_nested") == {"present": None, "exact": 9007199254740993}
assert value.get("static_headers", {}).get("X-API-Owned") == "preserve"
card = value.get("agent_card_params", {})
assert card.get("description") == "adversarial unrelated card update"
signatures = card.get("signatures", [])
assert signatures == [
    {"protected": "api-null", "signature": "api", "header": None},
    {"protected": "api-omitted", "signature": "api"},
]
skills = {skill["id"]: skill for skill in card.get("skills", [])}
assert "security" in skills["api-null"] and skills["api-null"]["security"] is None
assert "security" not in skills["api-omitted"]
PY
  STEADY_ARGS='-var=agent_lifecycle_phase=adversarial'
  CLEANUP_ARGS=$STEADY_ARGS
fi

echo '=== REFRESH CANONICAL STATE ==='
# Data-source inventories read during the initial plan before their producer
# resources exist. Persist the authoritative post-apply reads, then require a
# second plan to be exactly stable.
# shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
terraform apply -refresh-only -auto-approve $STEADY_ARGS
# Preserve the complete post-command state before any later plan or destroy.
# The supervisor is invoked immediately, so its phase receipt binds to this
# successful refresh-only command rather than a later show/state-list command.
cp terraform.tfstate matrix-refresh-state.tfstate
if [ -n "${MATRIX_EVIDENCE_SESSION:-}" ] && [ -n "${MATRIX_HARNESS:-}" ] && [ "${SMOKE_SUPPLEMENTAL_ONLY:-0}" != "1" ]; then
  set -- capture-refresh-phase --session "$MATRIX_EVIDENCE_SESSION" \
    --plan matrix-initial-plan.json --refresh-state matrix-refresh-state.tfstate \
    --cli "$MATRIX_EXECUTED_CLI"
  [ -z "$STEADY_ARGS" ] || set -- "$@" "--refresh-argument=$STEADY_ARGS"
  python3 "$MATRIX_HARNESS" "$@"
fi
terraform show -json >matrix-refreshed-state.json

echo '=== NO-DRIFT PLAN ==='
set +e
# shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
terraform plan -detailed-exitcode -out=matrix-steady.tfplan $STEADY_ARGS >steady-plan.log 2>&1
plan_status=$?
set -e
cat steady-plan.log
if [ "$plan_status" -ne 0 ]; then
  if [ "$plan_status" -eq 2 ]; then
    echo 'Smoke failed: post-apply plan contains drift.' >&3
  fi
  exit "$plan_status"
fi
terraform show -json matrix-steady.tfplan >matrix-steady-plan.json

if [ "${SMOKE_GUARDRAIL_EXTERNAL_DELETE:-}" = "1" ]; then
  echo '=== GUARDRAIL EXTERNAL DELETE AND AUTHORITATIVE 404 REFRESH ==='
  guardrail_delete_address=${SMOKE_GUARDRAIL_DELETE_ADDRESS:-}
  [ "$guardrail_delete_address" = "litellm_guardrail.safe_read" ] || {
    echo 'Guardrail external-delete mode requires the reviewed dedicated resource address.' >&3
    exit 1
  }
  [ -f resource_guardrail_safe_read_minimal.tf ] || {
    echo 'Guardrail external-delete mode requires only the dedicated safe-read fixture.' >&3
    exit 1
  }
  guardrail_delete_id=$(terraform output -raw guardrail_safe_read_id)
  guardrail_delete_url=$(python3 - "$guardrail_delete_id" <<'PY'
import sys, urllib.parse
identity=sys.argv[1]
if not identity or "/" in identity:
    raise SystemExit(1)
print("http://localhost:4000/guardrails/" + urllib.parse.quote(identity, safe=""))
PY
)
  unset guardrail_delete_id
  curl --fail --silent --show-error -X DELETE \
    -H 'Authorization: Bearer sk-testing-key' \
    "$guardrail_delete_url" >/dev/null
  unset guardrail_delete_url
  terraform apply -refresh-only -auto-approve >guardrail-external-delete-refresh.log 2>&1
  cat guardrail-external-delete-refresh.log
  terraform state list >matrix-final-state.list
  if grep -Fxq "$guardrail_delete_address" matrix-final-state.list; then
    echo 'Exact 404 refresh retained the externally deleted Guardrail address.' >&3
    exit 1
  fi
  [ ! -s matrix-final-state.list ] || {
    echo 'Guardrail external-delete refresh left unexpected managed state.' >&3
    exit 1
  }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || {
    echo 'Guardrail smoke log exceeded its private bound.' >&3
    exit 1
  }
  SUCCESS=1
  printf '\nSmoke passed: Guardrail create/refresh, direct delete, and exact-404 state removal succeeded.\n' >&3
  exit 0
fi

if [ "${SMOKE_KEY_EXTERNAL_DELETE:-}" = "1" ]; then
  echo '=== KEY EXTERNAL DELETE AND AUTHORITATIVE 404 REFRESH ==='
  key_delete_address=${SMOKE_KEY_DELETE_ADDRESS:-}
  key_block_delete_address=${SMOKE_KEY_BLOCK_DELETE_ADDRESS:-}
  [ "$key_delete_address" = "litellm_key.minimal" ] && [ "$key_block_delete_address" = "litellm_key_block.minimal" ] || {
    echo 'Key external-delete mode requires the reviewed key and key-block addresses.' >&3
    exit 1
  }
  [ -f resource_key_minimal.tf ] && [ -f resource_key_block_minimal.tf ] || {
    echo 'Key external-delete mode requires only the minimal key and key-block fixtures.' >&3
    exit 1
  }
  terraform output -raw key_minimal_key | python3 -c '
import json, sys
identity=sys.stdin.read()
if not identity:
    raise SystemExit(1)
json.dump({"keys": [identity]}, sys.stdout, separators=(",", ":"))
' >key-external-delete-request.json
  chmod 600 key-external-delete-request.json
  curl --fail --silent --show-error -X POST \
    -H 'Authorization: Bearer sk-testing-key' \
    -H 'Content-Type: application/json' \
    --data-binary @key-external-delete-request.json \
    'http://localhost:4000/key/delete' >/dev/null
  rm -f key-external-delete-request.json
  terraform apply -refresh-only -auto-approve >key-external-delete-refresh.log 2>&1
  cat key-external-delete-refresh.log
  terraform state list >matrix-final-state.list
  if grep -Fxq "$key_delete_address" matrix-final-state.list || grep -Fxq "$key_block_delete_address" matrix-final-state.list; then
    echo 'Exact 404 refresh retained an externally deleted Key or Key Block address.' >&3
    exit 1
  fi
  [ ! -s matrix-final-state.list ] || {
    echo 'Key external-delete refresh left unexpected managed state.' >&3
    exit 1
  }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || {
    echo 'Key smoke log exceeded its private bound.' >&3
    exit 1
  }
  SUCCESS=1
  printf '\nSmoke passed: Key and Key Block create/refresh, direct delete, and exact-404 state removal succeeded.\n' >&3
  exit 0
fi

if [ "${SMOKE_USER_EXTERNAL_DELETE:-}" = "1" ]; then
  echo '=== USER EXTERNAL DELETE AND AUTHORITATIVE 404 REFRESH ==='
  user_delete_address=${SMOKE_USER_DELETE_ADDRESS:-}
  [ "$user_delete_address" = "litellm_user.minimal" ] || {
    echo 'User external-delete mode requires the reviewed minimal resource address.' >&3
    exit 1
  }
  [ -f resource_user_minimal.tf ] || {
    echo 'User external-delete mode requires only the minimal resource fixture.' >&3
    exit 1
  }
  user_delete_id=$(terraform output -raw user_minimal_id)
  python3 - "$user_delete_id" >user-external-delete-request.json <<'PY'
import json, sys
identity=sys.argv[1]
if not identity:
    raise SystemExit(1)
json.dump({"user_ids": [identity]}, sys.stdout, separators=(",", ":"))
PY
  chmod 600 user-external-delete-request.json
  curl --fail --silent --show-error -X POST \
    -H 'Authorization: Bearer sk-testing-key' \
    -H 'Content-Type: application/json' \
    --data-binary @user-external-delete-request.json \
    'http://localhost:4000/user/delete' >/dev/null
  rm -f user-external-delete-request.json
  terraform apply -refresh-only -auto-approve >user-external-delete-refresh.log 2>&1
  cat user-external-delete-refresh.log
  terraform state list >matrix-final-state.list
  if grep -Fxq "$user_delete_address" matrix-final-state.list; then
    echo 'Exact 404 refresh retained the externally deleted User address.' >&3
    exit 1
  fi
  [ ! -s matrix-final-state.list ] || {
    echo 'User external-delete refresh left unexpected managed state.' >&3
    exit 1
  }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || {
    echo 'User smoke log exceeded its private bound.' >&3
    exit 1
  }
  SUCCESS=1
  printf '\nSmoke passed: User create/refresh, direct delete, and exact-404 state removal succeeded.\n' >&3
  exit 0
fi

if [ "${SMOKE_ACCESS_GROUP_EXTERNAL_DELETE:-}" = "1" ]; then
  echo '=== ACCESS GROUP EXTERNAL DELETE AND AUTHORITATIVE 404 REFRESH ==='
  access_group_delete_address=${SMOKE_ACCESS_GROUP_DELETE_ADDRESS:-}
  [ "$access_group_delete_address" = "litellm_access_group.minimal" ] || {
    echo 'Access Group external-delete mode requires the reviewed minimal resource address.' >&3
    exit 1
  }
  [ -f resource_model_access_group.tf ] && [ -f resource_access_group_minimal.tf ] || {
    echo 'Access Group external-delete mode requires the reviewed model and minimal resource fixtures.' >&3
    exit 1
  }
  access_group_delete_id=$(terraform output -raw access_group_minimal_id)
  access_group_delete_url=$(python3 - "$access_group_delete_id" <<'PY'
import sys, urllib.parse
identity=sys.argv[1]
if not identity or "/" in identity:
    raise SystemExit(1)
print("http://localhost:4000/access_group/" + urllib.parse.quote(identity, safe="") + "/delete")
PY
)
  curl --fail --silent --show-error -X DELETE \
    -H 'Authorization: Bearer sk-testing-key' \
    "$access_group_delete_url" >/dev/null
  terraform apply -refresh-only -auto-approve >access-group-external-delete-refresh.log 2>&1
  cat access-group-external-delete-refresh.log
  terraform state list >access-group-post-delete-state.list
  if grep -Fxq "$access_group_delete_address" access-group-post-delete-state.list; then
    echo 'Exact 404 refresh retained the externally deleted Access Group address.' >&3
    exit 1
  fi
  [ "$(cat access-group-post-delete-state.list)" = "litellm_model.access_group" ] || {
    echo 'Access Group external-delete refresh changed unexpected managed state.' >&3
    exit 1
  }
  terraform destroy -auto-approve >access-group-external-delete-destroy.log 2>&1
  cat access-group-external-delete-destroy.log
  terraform state list >matrix-final-state.list
  [ ! -s matrix-final-state.list ] || {
    echo 'Access Group external-delete cleanup left managed state.' >&3
    exit 1
  }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || {
    echo 'Access Group smoke log exceeded its private bound.' >&3
    exit 1
  }
  SUCCESS=1
  printf '\nSmoke passed: Access Group create/refresh, direct delete, exact-404 state removal, and model cleanup succeeded.\n' >&3
  exit 0
fi

if [ "${SMOKE_SEARCH_TOOL_EXTERNAL_DELETE:-}" = "1" ]; then
  echo '=== SEARCH TOOL EXTERNAL DELETE AND AUTHORITATIVE 404 REFRESH ==='
  search_tool_delete_address=${SMOKE_SEARCH_TOOL_DELETE_ADDRESS:-}
  [ "$search_tool_delete_address" = "litellm_search_tool.minimal" ] || {
    echo 'Search Tool external-delete mode requires the reviewed minimal resource address.' >&3
    exit 1
  }
  [ -f resource_search_tool_minimal.tf ] || {
    echo 'Search Tool external-delete mode requires only the minimal resource fixture.' >&3
    exit 1
  }
  search_tool_delete_id=$(terraform output -raw search_tool_minimal_id)
  search_tool_delete_url=$(python3 - "$search_tool_delete_id" <<'PY'
import sys, urllib.parse
identity=sys.argv[1]
if not identity or "/" in identity:
    raise SystemExit(1)
print("http://localhost:4000/search_tools/" + urllib.parse.quote(identity, safe=""))
PY
)
  curl --fail --silent --show-error -X DELETE \
    -H 'Authorization: Bearer sk-testing-key' \
    "$search_tool_delete_url" >/dev/null
  terraform apply -refresh-only -auto-approve >search-tool-external-delete-refresh.log 2>&1
  cat search-tool-external-delete-refresh.log
  terraform state list >matrix-final-state.list
  if grep -Fxq "$search_tool_delete_address" matrix-final-state.list; then
    echo 'Exact 404 refresh retained the externally deleted Search Tool address.' >&3
    exit 1
  fi
  [ ! -s matrix-final-state.list ] || {
    echo 'Search Tool external-delete refresh left unexpected managed state.' >&3
    exit 1
  }
  [ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || {
    echo 'Search Tool smoke log exceeded its private bound.' >&3
    exit 1
  }
  SUCCESS=1
  printf '\nSmoke passed: Search Tool create/refresh, direct delete, and exact-404 state removal succeeded.\n' >&3
  exit 0
fi

fallback_delete_unconfirmed=0
fallback_delete_succeeded=0
if [ "${SMOKE_FALLBACK_DELETE_UNSUPPORTED:-}" = "1" ]; then
  fallback_delete_address=${SMOKE_FALLBACK_DELETE_ADDRESS:-}
  case "$fallback_delete_address" in
    litellm_fallback.minimal|'litellm_fallback.fallback_imported[0]') ;;
    *) echo 'Fallback skip requires one exact reviewed state address.' >&3; exit 1 ;;
  esac
  echo '=== FALLBACK DESTROY FAIL-CLOSED PROOF ==='
  set +e
  # shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
  terraform destroy -auto-approve $STEADY_ARGS >fallback-delete-unconfirmed.log 2>&1
  fallback_destroy_status=$?
  set -e
  cat fallback-delete-unconfirmed.log
  if [ "$fallback_destroy_status" -ne 0 ]; then
    fallback_delete_unconfirmed=1
    python3 "$INTERNAL_TESTING/upgrade_matrix/fallback_delete_diagnostic.py" \
      fallback-delete-unconfirmed.log >/dev/null
    echo '=== FALLBACK AUTHORITATIVE PRESENCE REFRESH ==='
    set +e
    # shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
    terraform apply -refresh-only -auto-approve $STEADY_ARGS >fallback-presence-refresh.log 2>&1
    fallback_presence_status=$?
    set -e
    cat fallback-presence-refresh.log
    [ "$fallback_presence_status" -eq 0 ] || {
      echo 'Fallback presence could not be authoritatively refreshed after the failed delete.' >&3
      exit 1
    }
    terraform show -json >fallback-presence-state.json
    python3 - matrix-refreshed-state.json fallback-presence-state.json "$fallback_delete_address" <<'PY'
import json,sys
def find(path,address):
    value=json.load(open(path,encoding="utf-8")); found=[]
    def walk(module):
        for item in module.get("resources",[]):
            if item.get("address")==address: found.append(item)
        for child in module.get("child_modules",[]): walk(child)
    walk(value.get("values",{}).get("root_module",{}))
    return found
before=find(sys.argv[1],sys.argv[3]); after=find(sys.argv[2],sys.argv[3])
if len(before)!=1 or len(after)!=1: raise SystemExit(1)
if before[0].get("type")!="litellm_fallback" or before[0].get("mode")!="managed": raise SystemExit(1)
if before[0].get("values")!=after[0].get("values"): raise SystemExit(1)
if any(not before[0]["values"].get(key) for key in ("id","model","fallback_type")): raise SystemExit(1)
PY
    if [ -n "${MATRIX_EVIDENCE_SESSION:-}" ] && [ -n "${MATRIX_HARNESS:-}" ] && [ "${SMOKE_SUPPLEMENTAL_ONLY:-0}" != "1" ]; then
      set -- capture-fallback-presence --session "$MATRIX_EVIDENCE_SESSION" \
        --before-state matrix-refreshed-state.json --after-state fallback-presence-state.json \
        --refresh-output fallback-presence-refresh.log --delete-output fallback-delete-unconfirmed.log \
        --address "$fallback_delete_address" --cli "$MATRIX_EXECUTED_CLI"
      if [ -n "$STEADY_ARGS" ]; then
        set -- "$@" "--refresh-argument=$STEADY_ARGS" "--delete-argument=$STEADY_ARGS"
      fi
      python3 "$MATRIX_HARNESS" "$@"
    fi
    terraform state list >fallback-delete-retained-state.list
    grep -Fxq "$fallback_delete_address" fallback-delete-retained-state.list || {
      echo 'Fallback delete failure did not retain the managed address.' >&3
      exit 1
    }
    terraform state rm "$fallback_delete_address"
    echo '=== FALLBACK DEPENDENCY CLEANUP ==='
    # The pinned backend cannot remove the fallback. The disposable local stack
    # is the only remote cleanup boundary; Terraform destroys all representable
    # dependency objects after detaching only the known upstream-blocked address.
    # shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
    terraform destroy -auto-approve $STEADY_ARGS
  else
    fallback_delete_succeeded=1
  fi
else
  echo '=== DESTROY ==='
  # shellcheck disable=SC2086 # STEADY_ARGS intentionally expands to an optional complete argument.
  terraform destroy -auto-approve $STEADY_ARGS
fi

terraform state list >matrix-final-state.list
if [ -s matrix-final-state.list ]; then
  echo 'Smoke failed: state is not empty after destroy.' >&3
  exit 1
fi
[ "$(wc -c <"$SMOKE_LOG")" -le 10485760 ] || { echo 'Smoke log exceeded its private bound.' >&3; exit 1; }
if [ -n "${MATRIX_EVIDENCE_SESSION:-}" ] && [ -n "${MATRIX_HARNESS:-}" ] && [ "${SMOKE_SUPPLEMENTAL_ONLY:-0}" != "1" ]; then
  set -- observe-smoke --session "$MATRIX_EVIDENCE_SESSION" \
    --plan matrix-initial-plan.json --refresh-state matrix-refresh-state.tfstate \
    --state matrix-refreshed-state.json --steady-plan matrix-steady-plan.json \
    --final-state matrix-final-state.list
  [ "$fallback_delete_unconfirmed" -ne 1 ] || set -- "$@" --fallback-delete-unconfirmed \
    --fallback-delete-evidence fallback-delete-unconfirmed.log \
    --fallback-presence-state fallback-presence-state.json \
    --fallback-presence-output fallback-presence-refresh.log
  if [ "$fallback_delete_succeeded" -eq 1 ]; then
    set -- "$@" --fallback-delete-success-evidence fallback-delete-unconfirmed.log \
      --fallback-delete-success-cli "$MATRIX_EXECUTED_CLI"
    [ -z "$STEADY_ARGS" ] || set -- "$@" "--fallback-delete-success-argument=$STEADY_ARGS"
  fi
  python3 "$MATRIX_HARNESS" "$@"
fi

SUCCESS=1
if [ "$fallback_delete_unconfirmed" -eq 1 ]; then
  printf '\nSmoke passed: plan/apply/no-drift succeeded; authoritative fallback presence was recorded as an upstream skip.\n' >&3
elif [ "$fallback_delete_succeeded" -eq 1 ]; then
  printf '\nSmoke passed: plan/apply/no-drift and authoritative fallback deletion succeeded.\n' >&3
else
  printf '\nSmoke passed: plan, apply, no-drift plan, and destroy succeeded.\n' >&3
fi
echo "Results written to $SMOKE_LOG" >&3
