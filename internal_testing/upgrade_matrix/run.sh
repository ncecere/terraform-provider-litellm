#!/bin/sh
# Upgrade/import matrix entrypoint. Child output is captured in private scratch
# space and is never copied to artifacts or stdout.
set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd -P)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd -P)
MODE=${1:-assembly}
CLI=${MATRIX_CLI:-terraform}
REPORT=${MATRIX_REPORT:-$REPO_ROOT/internal_testing/upgrade-matrix-results.json}
EVIDENCE_REPORT=${MATRIX_EVIDENCE_REPORT:-${REPORT%.json}.evidence.jsonl}
PROVIDER_BINARY=${MATRIX_PROVIDER_BINARY:-$REPO_ROOT/terraform-provider-litellm}
CACHE=${MATRIX_CACHE:-${HOME}/.cache/terraform-provider-litellm}
SCRATCH=
LOG=
CLEANUP_MODE=none
WORKSPACE=
PRODUCER_WORKSPACE=
IMPORTER_WORKSPACE=
IMPORT_ADDRESS=
IMPORT_RESOURCE_TYPE=
IMPORT_ID_FILE=
PROXY_PID=
SESSION=
MATRIX_LEDGER_KEY_FILE=
COMMAND_TIMEOUT=${MATRIX_COMMAND_TIMEOUT:-300}
case $COMMAND_TIMEOUT in ''|*[!0-9]*) printf '%s\n' 'Matrix failed: invalid command timeout' >&2; exit 1 ;; esac
[ "$COMMAND_TIMEOUT" -ge 1 ] && [ "$COMMAND_TIMEOUT" -le 900 ] || { printf '%s\n' 'Matrix failed: command timeout is out of bounds' >&2; exit 1; }
run_cli() {
  python3 "$SCRIPT_DIR/deadline.py" --seconds "$COMMAND_TIMEOUT" "$CLI" "$@"
}
run_bounded() {
  python3 "$SCRIPT_DIR/deadline.py" --seconds "$COMMAND_TIMEOUT" "$@"
}

fail() {
  printf '%s\n' "Matrix failed: $1" >&2
  exit 1
}

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  cleanup_status=0
  if [ -n "$PROXY_PID" ]; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
    PROXY_PID=
  fi
  if [ -n "$IMPORTER_WORKSPACE" ] && [ -d "$IMPORTER_WORKSPACE" ]; then
    importer_addresses=$(cd "$IMPORTER_WORKSPACE" && run_cli state list 2>>"$LOG") || cleanup_status=$?
    if [ "$cleanup_status" -eq 0 ]; then
      for address in $importer_addresses; do
        (cd "$IMPORTER_WORKSPACE" && run_cli state rm "$address") >>"$LOG" 2>&1 || cleanup_status=$?
      done
    fi
  fi
  if [ -n "$PRODUCER_WORKSPACE" ] && [ -d "$PRODUCER_WORKSPACE" ]; then
    producer_attempt=1
    producer_status=1
    while [ "$producer_attempt" -le 2 ]; do
      if (cd "$PRODUCER_WORKSPACE" && run_cli destroy -refresh=false -auto-approve) >>"$LOG" 2>&1 &&
         [ -z "$(cd "$PRODUCER_WORKSPACE" && run_cli state list 2>>"$LOG")" ]; then
        producer_status=0
        break
      fi
      producer_attempt=$((producer_attempt + 1))
    done
    [ "$producer_status" -eq 0 ] || cleanup_status=1
    if [ "$producer_status" -eq 0 ] && [ -n "$IMPORTER_WORKSPACE" ] && [ -d "$IMPORTER_WORKSPACE" ] && [ -s "$IMPORT_ID_FILE" ]; then
      set +e
      (cd "$IMPORTER_WORKSPACE" && run_cli import "$IMPORT_ADDRESS" "$(cat "$IMPORT_ID_FILE")") >"$SCRATCH/controller-absence.out" 2>&1
      controller_absence_status=$?
      set -e
      cat "$SCRATCH/controller-absence.out" >>"$LOG"
      if [ "$controller_absence_status" -eq 0 ]; then
        cleanup_status=1
      else
        assert_authoritative_not_found "$SCRATCH/controller-absence.out" "$IMPORT_RESOURCE_TYPE" || cleanup_status=1
      fi
    fi
  fi
  if [ -n "$WORKSPACE" ] && [ "$WORKSPACE" != "$PRODUCER_WORKSPACE" ] && [ -d "$WORKSPACE" ]; then
    if [ "$CLEANUP_MODE" = import ]; then
      # Imported/pre-existing objects are detached only. Never substitute destroy.
      addresses=$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG" || cleanup_status=$?)
      if [ "$cleanup_status" -eq 0 ]; then
        for address in $addresses; do
          (cd "$WORKSPACE" && run_cli state rm "$address") >>"$LOG" 2>&1 || cleanup_status=$?
        done
      fi
    elif [ "$CLEANUP_MODE" = owned ]; then
      cleanup_attempt=1
      cleanup_status=1
      while [ "$cleanup_attempt" -le 2 ]; do
        if (cd "$WORKSPACE" && run_cli destroy -refresh=false -auto-approve) >>"$LOG" 2>&1 &&
           [ -z "$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG")" ]; then
          cleanup_status=0
          break
        fi
        cleanup_attempt=$((cleanup_attempt + 1))
      done
    fi
  fi
  # A nested smoke workspace is a real producer/importer state holder. Retry
  # cleanup and authoritative empty-state checks instead of relying on hosted
  # stack teardown. Preserve every state/log if any retry remains incomplete.
  if [ -n "$SCRATCH" ] && [ -d "$SCRATCH/smoke" ]; then
    for smoke_workspace in "$SCRATCH"/smoke/.smoke.*; do
      [ -d "$smoke_workspace" ] || continue
      smoke_status=1
      smoke_attempt=1
      while [ "$smoke_attempt" -le 2 ]; do
        smoke_addresses=$(cd "$smoke_workspace" && terraform state list 2>>"$LOG") && smoke_list_status=0 || smoke_list_status=$?
        if [ "$smoke_list_status" -eq 0 ] && [ -z "$smoke_addresses" ]; then
          smoke_status=0
          break
        fi
        if (cd "$smoke_workspace" && terraform destroy -refresh=false -auto-approve) >>"$LOG" 2>&1 &&
           [ -z "$(cd "$smoke_workspace" && terraform state list 2>>"$LOG")" ]; then
          smoke_status=0
          break
        fi
        smoke_attempt=$((smoke_attempt + 1))
      done
      [ "$smoke_status" -eq 0 ] || cleanup_status=1
    done
  fi
  # The signing key is never recovery material. Remove it through the harness'
  # no-follow directory-FD path whether cleanup succeeded or recovery state
  # must be retained. A removal failure itself fails closed.
  if [ -n "$SESSION" ] && [ -f "$SESSION" ] && [ -n "$MATRIX_LEDGER_KEY_FILE" ]; then
    python3 "$SCRIPT_DIR/harness.py" remove-session-key --session "$SESSION" >/dev/null 2>&1 || cleanup_status=1
    MATRIX_LEDGER_KEY_FILE=
  fi
  if [ "$cleanup_status" -ne 0 ]; then
    printf '%s\n' 'Matrix failed: cleanup did not complete; only private recovery state was retained' >&2
    exit 1
  fi
  # A successful report removes every raw artifact. Failed execution retains
  # bounded diagnostics/recovery state, but never the session signing key.
  if [ "$status" -eq 0 ]; then
    [ -z "$SCRATCH" ] || rm -rf "$SCRATCH"
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

if [ "$MODE" = assembly ]; then
  if [ -f "$PROVIDER_BINARY" ]; then
    exec python3 "$SCRIPT_DIR/harness.py" assembly --cli "$CLI" --provider-binary "$PROVIDER_BINARY" --report "$REPORT"
  fi
  exec python3 "$SCRIPT_DIR/harness.py" assembly --cli "$CLI" --report "$REPORT"
fi

[ "$MODE" = local ] || [ "$MODE" = remote ] || fail 'mode must be assembly, local, or remote'
python3 "$SCRIPT_DIR/harness.py" preflight "$MODE"
[ -x "$PROVIDER_BINARY" ] || fail 'current provider binary is required'
SOURCE_PROVIDER_BINARY=$PROVIDER_BINARY

SCRATCH=$(python3 "$SCRIPT_DIR/harness.py" make-private-temp --base "${TMPDIR:-/tmp}") || fail 'temporary root contains an untrusted alias'
CACHE=$(python3 "$SCRIPT_DIR/harness.py" canonical-path --path "$CACHE") || fail 'cache root contains an untrusted alias'
chmod 700 "$SCRATCH"
provider_dir=$(python3 "$SCRIPT_DIR/harness.py" prepare-provider \
  --provider-binary "$SOURCE_PROVIDER_BINARY" --directory "$SCRATCH/provider-cache") || fail 'provider private bundle verification failed'
PROVIDER_BINARY=$provider_dir/terraform-provider-litellm
[ -x "$PROVIDER_BINARY" ] || fail 'verified provider executable is unavailable'
LOG=$SCRATCH/matrix.log
: >"$LOG"
chmod 600 "$LOG"
# deadline.py bounds every selected CLI command's merged output before this
# private aggregate log receives it. Do not use a process-wide file-size limit:
# Terraform inherits it and must extract signed provider executables over 10 MiB.
COMMAND_LEDGER=$SCRATCH/evidence.jsonl
SESSION=$SCRATCH/session.json
session_value=$(python3 "$SCRIPT_DIR/harness.py" start-session \
  --ledger "$COMMAND_LEDGER" --session "$SESSION" --cli "$CLI" \
  --provider-binary "$PROVIDER_BINARY") || fail 'trusted evidence supervisor initialization failed'
export MATRIX_COMMAND_LEDGER=$COMMAND_LEDGER MATRIX_EVIDENCE_SESSION=$SESSION
export MATRIX_RUN_NONCE=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["run_nonce"])')
export MATRIX_CLI_LANE=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["cli_lane"])')
export MATRIX_CANDIDATE_COMMIT=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["candidate_commit"])')
export MATRIX_PROVIDER_SHA256=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["provider_sha256"])')
export MATRIX_PROVIDER_SCHEMA_SHA256=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["provider_schema_sha256"])')
export MATRIX_HARNESS_SHA256=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["harness_sha256"])')
export MATRIX_MATRIX_SHA256=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["matrix_sha256"])')
export MATRIX_LEDGER_KEY_FILE=$(printf '%s' "$session_value" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key_file"])')
export MATRIX_HARNESS=$SCRIPT_DIR/harness.py MATRIX_PRIVATE_LOG=$LOG

# Ambient logging/CLI arguments can expose bodies or redirect the backend.
unset TF_LOG TF_LOG_PATH TF_CLI_ARGS TF_CLI_ARGS_init TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_CLI_ARGS_destroy
export TF_IN_AUTOMATION=1 TF_INPUT=0 TF_CLI_ARGS=-no-color

if [ "$MODE" = local ]; then
  [ "${LITELLM_API_BASE:-http://localhost:4000}" = 'http://localhost:4000' ] || fail 'local target must be loopback port 4000'
  [ "${LITELLM_API_KEY:-sk-testing-key}" = 'sk-testing-key' ] || fail 'local target must use the disposable stack credential'
  grep -q 'docker.litellm.ai/berriai/litellm:v1.98.0' "$REPO_ROOT/internal_testing/docker-compose.yml" || fail 'local image is not the exact pinned release'
  version=$(curl --fail --silent --show-error --connect-timeout 3 --max-time 15 'http://localhost:4000/openapi.json' 2>>"$LOG" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("info",{}).get("version",""))')
  [ "$version" = '1.98.0' ] || fail 'local LiteLLM version check failed'
  export TF_VAR_litellm_api_base='http://localhost:4000'
  export TF_VAR_litellm_api_key='sk-testing-key'
else
  [ "${LITELLM_API_BASE:-}" = 'https://dev.api.ai.it.ufl.edu' ] || fail 'remote target does not match the approved development deployment'
  [ -n "${LITELLM_API_KEY:-}" ] || fail 'remote credential is required'
  export TF_VAR_litellm_api_base=$LITELLM_API_BASE
  export TF_VAR_litellm_api_key=$LITELLM_API_KEY
fi

selected_cli_version=$(run_cli version | python3 -c 'import re,sys; m=re.search(r"\d+\.\d+\.\d+",sys.stdin.read()); print(m.group(0) if m else "")')
[ -n "$selected_cli_version" ] || fail 'selected CLI did not report a semantic version'
CLI_SUPPORTS_111=$(python3 - "$selected_cli_version" <<'PY'
import sys
print(1 if tuple(int(value) for value in sys.argv[1].split(".")) >= (1,11,0) else 0)
PY
)

if [ "${MATRIX_OFFLINE:-0}" = 1 ]; then
  python3 "$SCRIPT_DIR/harness.py" install-previous --cache "$CACHE/providers" --offline >>"$LOG" 2>&1
else
  python3 "$SCRIPT_DIR/harness.py" install-previous --cache "$CACHE/providers" >>"$LOG" 2>&1
fi
PREVIOUS_MIRROR=$CACHE/providers/mirror
provider_platform=$(python3 - <<'PY'
import platform
system={"Darwin":"darwin","Linux":"linux"}.get(platform.system())
machine={"x86_64":"amd64","arm64":"arm64","aarch64":"arm64"}.get(platform.machine())
if not system or not machine: raise SystemExit(1)
print(system+"_"+machine)
PY
) || fail 'previous provider platform is unsupported'
PREVIOUS_PROVIDER_BINARY=$CACHE/providers/extracted/$provider_platform/v2.0.1/terraform-provider-litellm_v2.0.1
[ -x "$PREVIOUS_PROVIDER_BINARY" ] || fail 'verified previous provider executable is unavailable'

write_provider_config() {
  destination=$1
  cat >"$destination/provider.tf" <<'TF'
terraform {
  required_version = ">= 1.1.0"
  required_providers {
    litellm = {
      source  = "registry.terraform.io/ncecere/litellm"
      version = "= 2.0.1"
    }
  }
}
variable "litellm_api_base" { type = string }
variable "litellm_api_key" {
  type      = string
  sensitive = true
}
provider "litellm" {
  api_base = var.litellm_api_base
  api_key  = var.litellm_api_key
}
TF
}

write_old_config() {
  cat >"$1" <<EOF
provider_installation {
  filesystem_mirror {
    path    = $(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$PREVIOUS_MIRROR")
    include = ["registry.terraform.io/ncecere/litellm"]
  }
}
EOF
}

write_current_config() {
  cat >"$1" <<EOF
provider_installation {
  dev_overrides {
    "registry.terraform.io/ncecere/litellm" = $(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$(dirname "$PROVIDER_BINARY")")
  }
}
EOF
}

record() {
  # The supervisor accepts only checked-matrix labels and binds every scenario
  # to the latest bounded command receipt plus an observed private artifact.
  name=$1 category=$2 status=$3 reason=$4
  observation_evidence=${SCENARIO_EVIDENCE:-$LOG}
  SCENARIO_EVIDENCE=
  log_size=$(wc -c <"$LOG")
  [ "$log_size" -le 10485760 ] || fail 'private child log exceeded its bounded size'
  assertion=terraform-plan-state-api
  result_code=${6:-}
  case "$category" in
    upgrade)
      assertion=upgrade-state-migration
      case "$result_code" in
        upgrade-reviewed-private-plan-trigger-migration)
          assertion=upgrade-private-plan-trigger-migration ;;
        ''|upgrade-reviewed-migration) ;;
        *) fail 'upgrade emitted a non-controlled migration result' ;;
      esac
      ;;
    import) assertion=import-authoritative-absence ;;
    replacement) assertion=replacement-plan-state ;;
    failure_recovery) assertion=fault-endpoint-diagnostic-state ;;
    optional_feature) assertion=bounded-feature-attempt ;;
    documentation) assertion=validated-documentation ;;
  esac
  [ "$status" != skipped ] || assertion=allowlisted-unavailability
  diagnostic=
  case "$name" in
    failure_recovery:model_failed_create_retry) diagnostic=model-create-error ;;
    failure_recovery:team_failed_create_retry) diagnostic=team-create-error ;;
    import:litellm_agent) [ "$status" != skipped ] || diagnostic=agent-role-redacted-read ;;
    optional_feature:key_wo) [ "$status" != skipped ] || diagnostic=key-write-only-endpoint-unavailable ;;
  esac
  set -- --session "$SESSION" --name "$name" --category "$category" --status "$status" --assertion "$assertion" --evidence "$observation_evidence"
  [ -z "$reason" ] || set -- "$@" --reason "$reason"
  [ -z "$diagnostic" ] || set -- "$@" --diagnostic-code "$diagnostic"
  python3 "$SCRIPT_DIR/harness.py" record-observation "$@" || fail 'trusted scenario observation was rejected'
  printf '%-18s %-38s %s\n' "$category" "$name" "$status"
}

assemble_workspace() {
  resource_type=$1
  fixtures=$2
  WORKSPACE=$SCRATCH/work
  rm -rf "$WORKSPACE"
  mkdir -m 700 "$WORKSPACE"
  write_provider_config "$WORKSPACE"
  index=0
  old_ifs=$IFS
  IFS=,
  for fixture in $fixtures; do
    cp "$REPO_ROOT/internal_testing/resources/$fixture" "$WORKSPACE/fixture_$index.tf"
    index=$((index + 1))
  done
  IFS=$old_ifs
  (cd "$WORKSPACE" && run_cli fmt -check .) >>"$LOG" 2>&1 || return 1
}

compare_upgrade_states() {
  before=$1 after=$2 schema=$3 resource_type=$4 raw_before=$5 raw_after=$6 private_trigger=$7
  set -- compare \
    --before "$before" --after "$after" --schema "$schema" \
    --resource-type "$resource_type" --raw-before "$raw_before" \
    --raw-after "$raw_after" --matrix "$SCRIPT_DIR/matrix.json"
  [ "$private_trigger" != true ] || set -- "$@" --require-reviewed-private-migration
  python3 "$SCRIPT_DIR/upgrade_state.py" "$@"
}

run_upgrade() {
  resource_type=$1 fixtures=$2 lane=$3 introduced_after_previous=$4
  if [ "$introduced_after_previous" = true ]; then
    record "upgrade:$resource_type" upgrade skipped previous-release-resource-unavailable
    return
  fi
  if [ "$lane" = enterprise ] && [ "${LITELLM_ENTERPRISE_CONFIRM:-}" != licensed-disposable ]; then
    record "upgrade:$resource_type" upgrade skipped enterprise-license-required
    return
  fi
  assemble_workspace "$resource_type" "$fixtures" || fail 'upgrade fixture assembly failed'
  old_rc=$SCRATCH/old.tfrc
  current_rc=$SCRATCH/current.tfrc
  write_old_config "$old_rc"
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$old_rc
  (cd "$WORKSPACE" && run_cli init -backend=false -lockfile=readonly) >>"$LOG" 2>&1 || {
    # The first verified init creates the lock file; retry without readonly only for that operation.
    (cd "$WORKSPACE" && run_cli init -backend=false) >>"$LOG" 2>&1 || fail 'published-provider initialization failed'
  }
  grep -q 'version *= *"2.0.1"' "$WORKSPACE/.terraform.lock.hcl" || fail 'lock file did not select published 2.0.1'
  CLEANUP_MODE=owned
  (cd "$WORKSPACE" && run_cli apply -auto-approve) >>"$LOG" 2>&1 || fail 'previous-release baseline apply failed'
  (cd "$WORKSPACE" && run_cli apply -refresh-only -auto-approve) >>"$LOG" 2>&1 || fail 'previous-release canonical refresh failed'
  (cd "$WORKSPACE" && run_cli show -json) >"$SCRATCH/state-before.json" 2>>"$LOG" || fail 'baseline state inspection failed'
  (cd "$WORKSPACE" && run_cli state pull) >"$SCRATCH/raw-before.json" 2>>"$LOG" || fail 'baseline private-state inspection failed'
  export TF_CLI_CONFIG_FILE=$current_rc
  (cd "$WORKSPACE" && run_cli providers schema -json) >"$SCRATCH/current-schema.json" 2>>"$LOG" || fail 'current provider protocol/schema inspection failed'
  set +e
  (cd "$WORKSPACE" && run_cli plan -detailed-exitcode -refresh=true -out=current-upgrade.tfplan) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || [ "$plan_status" -eq 2 ] || fail 'current provider upgrade plan failed'
  (cd "$WORKSPACE" && run_cli show -json current-upgrade.tfplan) >"$SCRATCH/current-upgrade-plan.json" 2>>"$LOG" || fail 'current provider upgrade plan inspection failed'
  private_trigger_baseline=$SCRATCH/private-trigger-baseline.json
  plan_review=$(python3 "$SCRIPT_DIR/upgrade_state.py" review-plan \
    --plan "$SCRATCH/current-upgrade-plan.json" \
    --schema "$SCRATCH/current-schema.json" \
    --matrix "$SCRIPT_DIR/matrix.json" \
    --resource-type "$resource_type" \
    --private-trigger-baseline "$private_trigger_baseline") || fail 'upgrade proposed non-reviewed drift or replacement'
  private_trigger=false
  comparison_before=$SCRATCH/state-before.json
  case "$plan_review" in
    upgrade-plan-reviewed) ;;
    upgrade-reviewed-private-plan-trigger)
      private_trigger=true
      comparison_before=$private_trigger_baseline
      [ -s "$comparison_before" ] || fail 'reviewed private plan trigger lacks its public baseline'
      ;;
    *) fail 'upgrade plan review emitted a non-controlled result' ;;
  esac
  [ "$private_trigger" != true ] || [ "$plan_status" -eq 2 ] || \
    fail 'reviewed private plan trigger did not produce an update plan'
  if [ "$plan_status" -eq 2 ]; then
    # The JSON review above limits this convergence apply to exact per-type
    # migration fields and rejects replacement or unrelated actions.
    (cd "$WORKSPACE" && run_cli apply -auto-approve current-upgrade.tfplan) >>"$LOG" 2>&1 || fail 'reviewed upgrade convergence apply failed'
  fi
  (cd "$WORKSPACE" && run_cli apply -refresh-only -auto-approve) >>"$LOG" 2>&1 || fail 'current-provider refresh-only apply failed'
  (cd "$WORKSPACE" && run_cli show -json) >"$SCRATCH/state-after.json" 2>>"$LOG" || fail 'upgraded state inspection failed'
  (cd "$WORKSPACE" && run_cli state pull) >"$SCRATCH/raw-after.json" 2>>"$LOG" || fail 'upgraded private-state inspection failed'
  migration_code=$(compare_upgrade_states "$comparison_before" "$SCRATCH/state-after.json" \
    "$SCRATCH/current-schema.json" "$resource_type" "$SCRATCH/raw-before.json" \
    "$SCRATCH/raw-after.json" "$private_trigger") || fail 'canonical upgrade state contract failed'
  set +e
  (cd "$WORKSPACE" && run_cli plan -detailed-exitcode -refresh=true -out=final-upgrade.tfplan) >>"$LOG" 2>&1
  final_upgrade_status=$?
  set -e
  [ "$final_upgrade_status" -eq 0 ] || fail 'reviewed refresh migration did not reach final zero drift'
  (cd "$WORKSPACE" && run_cli destroy -auto-approve) >>"$LOG" 2>&1 || fail 'owned upgrade fixture cleanup failed'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  SCENARIO_EVIDENCE=$SCRATCH/state-after.json
  record "upgrade:$resource_type" upgrade passed '' '' "$migration_code"
}

assert_authoritative_not_found() {
  evidence=$1 resource_type=$2
  # Fail closed on exact Terraform Core wrappers, bounded provider API
  # diagnostics, and two reviewed endpoint-specific absence contracts. Generic
  # "not found" or HTTP-status text can describe unrelated plugin failures.
  python3 "$SCRIPT_DIR/absence_diagnostic.py" "$evidence" "$resource_type" || \
    fail 'post-destroy check was not an exact provider/API not-found result'
}

assert_agent_role_redaction_skip() {
  evidence=$1 agent_id_file=$2
  agent_status=$(curl --silent --show-error --connect-timeout 3 --max-time 15 \
    -H 'Authorization: Bearer sk-testing-key' -o "$SCRATCH/agent-role-response.json" \
    -w '%{http_code}' "http://localhost:4000/v1/agents/$(cat "$agent_id_file")") || fail 'agent role-redaction endpoint observation failed'
  [ "$agent_status" = 200 ] || fail 'agent role-redaction endpoint returned a non-allowlisted status'
  python3 - "$evidence" "$SCRATCH/agent-role-response.json" "$agent_id_file" <<'PY' || fail 'agent import failure was not the exact bounded role-redaction diagnostic/status'
from pathlib import Path
import json,sys
text=Path(sys.argv[1]).read_text(encoding="utf-8",errors="replace")
body=Path(sys.argv[2]).read_bytes(); expected=Path(sys.argv[3]).read_text(encoding="utf-8").strip()
if len(text.encode()) > 2*1024*1024 or len(body) > 1024*1024: raise SystemExit(1)
normalized=" ".join(text.lower().split())
if text.count("Error: Unsupported Agent Clear") != 1: raise SystemExit(1)
if "the complete provider block cannot be removed while it contains api-owned leaves" not in normalized: raise SystemExit(1)
value=json.loads(body)
if not isinstance(value,dict) or value.get("agent_id") != expected: raise SystemExit(1)
PY
  python3 - "$evidence" "$SCRATCH/agent-role-response.json" <<'PY' >"$SCRATCH/agent-role-observation.json"
import hashlib,json,sys
print(json.dumps({"schema_version":1,"endpoint_status":200,"diagnostic_sha256":hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest(),"response_sha256":hashlib.sha256(open(sys.argv[2],"rb").read()).hexdigest()},sort_keys=True))
PY
}

run_import() {
  resource_type=$1 producer_fixtures=$2 importer_fixtures=$3 address=$4 expression=$5 lane=$6 expected_skip=${7:-}
  IMPORT_ADDRESS=$address
  IMPORT_RESOURCE_TYPE=$resource_type
  if [ "$lane" = enterprise ] && [ "${LITELLM_ENTERPRISE_CONFIRM:-}" != licensed-disposable ]; then
    record "import:$resource_type" import skipped enterprise-license-required
    return
  fi
  if [ "$lane" = requires-1.11 ] && [ "$CLI_SUPPORTS_111" -ne 1 ]; then
    record "import:$resource_type" import skipped cli-version-below-1.11
    return
  fi
  [ "$MODE" = local ] || {
    record "import:$resource_type" import skipped inventory-endpoint-may-be-unavailable
    return
  }
  assemble_workspace "$resource_type" "$producer_fixtures" || fail 'import producer fixture assembly failed'
  producer=$WORKSPACE
  PRODUCER_WORKSPACE=$producer
  CLEANUP_MODE=owned
  # This namespace is generated for this producer/importer pair and never
  # emitted. It prevents collision with stale or concurrent scenario objects.
  namespace=$(python3 -c 'import secrets; print(secrets.token_hex(12))')
  python3 - "$producer" "$namespace" <<'PYNS'
from pathlib import Path
import sys
root=Path(sys.argv[1]); namespace=sys.argv[2]
for path in root.glob("fixture_*.tf"):
    text=path.read_text(encoding="utf-8")
    text=text.replace('"issue210-', f'"issue210-{namespace}-')
    text=text.replace('"smoke-', f'"smoke-{namespace}-')
    path.write_text(text, encoding="utf-8")
PYNS
  current_rc=$SCRATCH/current.tfrc
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$current_rc
  if echo "$expression" | grep -q '^composite_team_member_add'; then
    : # Derived below from producer state without displaying components.
  else
    cat >"$producer/matrix-output.tf" <<EOF
output "matrix_import_id" {
  value     = $expression
  sensitive = true
}
EOF
  fi
  (cd "$producer" && run_cli apply -auto-approve) >>"$LOG" 2>&1 || fail 'disposable import producer apply failed'
  if echo "$expression" | grep -q '^composite_team_member_add'; then
    (cd "$producer" && run_cli show -json) >"$SCRATCH/import-state.json" 2>>"$LOG" || fail 'producer identity inspection failed'
    python3 - "$SCRATCH/import-state.json" "$address" >"$SCRATCH/import-id" <<'PYID'
import base64,json,sys
value=json.load(open(sys.argv[1], encoding="utf-8")); wanted=sys.argv[2]
def enc(v): return base64.urlsafe_b64encode(v.encode()).decode().rstrip("=")
def walk(m):
  for r in m.get("resources",[]):
    if r.get("address")==wanted:
      v=r["values"]; tokens=[]
      for member in v.get("member",[]):
        if member.get("user_id"): tokens.append("i~"+enc(member["user_id"]))
        elif member.get("user_email"): tokens.append("e~"+enc(member["user_email"]))
      print("v1."+enc(v["team_id"])+"."+",".join(sorted(tokens))); return True
  return any(walk(c) for c in m.get("child_modules",[]))
if not walk(value["values"]["root_module"]): raise SystemExit(1)
PYID
  else
    (cd "$producer" && run_cli output -raw matrix_import_id) >"$SCRATCH/import-id" 2>>"$LOG" || fail 'producer identity capture failed'
  fi
  [ -s "$SCRATCH/import-id" ] || fail 'import identity was empty'
  IMPORT_ID_FILE=$SCRATCH/import-id

  importer=$SCRATCH/importer
  IMPORTER_WORKSPACE=$importer
  mkdir -m 700 "$importer"
  write_provider_config "$importer"
  index=0
  old_ifs=$IFS
  IFS=,
  for fixture in $importer_fixtures; do
    cp "$REPO_ROOT/internal_testing/resources/$fixture" "$importer/fixture_$index.tf"
    index=$((index + 1))
  done
  IFS=$old_ifs
  python3 - "$importer" "$namespace" <<'PYNS'
from pathlib import Path
import sys
root=Path(sys.argv[1]); namespace=sys.argv[2]
for path in root.glob("fixture_*.tf"):
    text=path.read_text(encoding="utf-8")
    text=text.replace('"issue210-', f'"issue210-{namespace}-')
    text=text.replace('"smoke-', f'"smoke-{namespace}-')
    path.write_text(text, encoding="utf-8")
PYNS
  (cd "$importer" && run_cli fmt -check .) >>"$LOG" 2>&1 || fail 'importer fixture assembly failed'
  # The producer remains authoritative owner. A private state snapshot gives
  # the importer dependency context without detaching anything from producer.
  cp "$producer/terraform.tfstate" "$importer/terraform.tfstate"
  (cd "$importer" && run_cli state rm "$address") >>"$LOG" 2>&1 || fail 'importer target preparation failed'
  (cd "$importer" && run_cli import "$address" "$(cat "$SCRATCH/import-id")") >>"$LOG" 2>&1 || fail 'import failed'
  set +e
  (cd "$importer" && run_cli refresh) >"$SCRATCH/import-refresh.out" 2>&1
  refresh_status=$?
  set -e
  cat "$SCRATCH/import-refresh.out" >>"$LOG"
  if [ "$refresh_status" -ne 0 ]; then
    [ "$resource_type" = litellm_agent ] && [ "$expected_skip" = role-redacted-state-requires-admin ] || fail 'post-import refresh failed outside the exact agent role-redaction allowance'
    assert_agent_role_redaction_skip "$SCRATCH/import-refresh.out" "$SCRATCH/import-id"
    # The endpoint-specific limitation was exercised, not inferred. Detach the
    # failed import, destroy only through the authoritative producer, and prove
    # both empty state and remote absence before recording an explicit skip.
    (cd "$importer" && run_cli state rm "$address") >>"$LOG" 2>&1 || fail 'limited import target detach failed'
    (cd "$producer" && run_cli destroy -auto-approve) >>"$LOG" 2>&1 || fail 'limited import producer cleanup failed'
    [ -z "$(cd "$producer" && run_cli state list 2>>"$LOG")" ] || fail 'limited import producer state was not empty'
    set +e
    (cd "$importer" && run_cli import "$address" "$(cat "$SCRATCH/import-id")") >"$SCRATCH/import-absence.out" 2>&1
    absence_status=$?
    set -e
    cat "$SCRATCH/import-absence.out" >>"$LOG"
    [ "$absence_status" -ne 0 ] || fail 'limited import target remained authoritative after destroy'
    assert_authoritative_not_found "$SCRATCH/import-absence.out" "$resource_type"
    CLEANUP_MODE=none
    rm -rf "$producer" "$importer"
    WORKSPACE=
    PRODUCER_WORKSPACE=
    IMPORTER_WORKSPACE=
    IMPORT_ADDRESS=
    IMPORT_RESOURCE_TYPE=
    IMPORT_ID_FILE=
    SCENARIO_EVIDENCE=$SCRATCH/agent-role-observation.json
    record "import:$resource_type" import skipped "$expected_skip"
    return
  fi
  set +e
  (cd "$importer" && run_cli plan -detailed-exitcode -out=import-converge.tfplan) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || [ "$plan_status" -eq 2 ] || fail 'post-import convergence plan failed'
  if [ "$plan_status" -eq 2 ]; then
    (cd "$importer" && run_cli show -json import-converge.tfplan) >"$SCRATCH/import-converge.json" 2>>"$LOG" || fail 'post-import convergence plan inspection failed'
    convergence_kind=$(python3 "$SCRIPT_DIR/import_convergence.py" \
      "$SCRATCH/import-converge.json" "$address") || \
      fail 'post-import convergence contained a non-reviewed action'
    # Imported state can lack provider-private configured markers. Terraform
    # 1.1 also reports removal of the producer-only matrix_import_id output as
    # detailed exit 2. Apply only an exact target update and/or that exact local
    # output deletion, then prove state and remote stability.
    case "$convergence_kind" in
      target-update|stale-output-delete) ;;
      *) fail 'post-import convergence classification was not reviewed' ;;
    esac
    (cd "$importer" && run_cli apply -auto-approve import-converge.tfplan) >>"$LOG" 2>&1 || fail 'post-import convergence apply failed'
    set +e
    (cd "$importer" && run_cli plan -detailed-exitcode) >>"$LOG" 2>&1
    plan_status=$?
    set -e
    [ "$plan_status" -eq 0 ] || fail 'post-import converged state contains drift'
  fi
  # Detach only the imported address. Producer still holds every object and
  # dependency in its untouched state and performs the eventual cleanup.
  (cd "$importer" && run_cli state rm "$address") >>"$LOG" 2>&1 || fail 'importer target detach failed'
  if (cd "$importer" && run_cli state list 2>>"$LOG" | grep -Fx "$address" >/dev/null); then
    fail 'imported address remained after target-only detach'
  fi
  (cd "$producer" && run_cli destroy -auto-approve) >>"$LOG" 2>&1 || fail 'producer-owned import cleanup failed'
  [ -z "$(cd "$producer" && run_cli state list 2>>"$LOG")" ] || fail 'producer state was not empty after cleanup'
  # An authoritative re-import after producer destroy must fail. This checks
  # absence without issuing a delete or adopting anything.
  set +e
  (cd "$importer" && run_cli import "$address" "$(cat "$SCRATCH/import-id")") >"$SCRATCH/import-absence.out" 2>&1
  absence_status=$?
  set -e
  cat "$SCRATCH/import-absence.out" >>"$LOG"
  [ "$absence_status" -ne 0 ] || fail 'destroyed import target remained authoritative'
  assert_authoritative_not_found "$SCRATCH/import-absence.out" "$resource_type"
  CLEANUP_MODE=none
  rm -rf "$producer" "$importer"
  WORKSPACE=
  PRODUCER_WORKSPACE=
  IMPORTER_WORKSPACE=
  IMPORT_ADDRESS=
  IMPORT_RESOURCE_TYPE=
  IMPORT_ID_FILE=
  SCENARIO_EVIDENCE=$SCRATCH/import-absence.out
  record "import:$resource_type" import passed ''
}

namespace_workspace() {
  directory=$1
  namespace=$(python3 -c 'import secrets; print(secrets.token_hex(12))')
  python3 - "$directory" "$namespace" <<'PY'
from pathlib import Path
import sys
for path in Path(sys.argv[1]).glob("*.tf"):
    text=path.read_text(encoding="utf-8")
    text=text.replace('"issue210-', f'"issue210-{sys.argv[2]}-')
    text=text.replace('"smoke-', f'"smoke-{sys.argv[2]}-')
    path.write_text(text,encoding="utf-8")
PY
}

assemble_matrix_fixture() {
  fixture=$1
  WORKSPACE=$SCRATCH/work
  rm -rf "$WORKSPACE"
  mkdir -m 700 "$WORKSPACE"
  write_provider_config "$WORKSPACE"
  cp "$SCRIPT_DIR/$fixture" "$WORKSPACE/scenario.tf"
  (cd "$WORKSPACE" && run_cli fmt -check .) >>"$LOG" 2>&1 || return 1
  current_rc=$SCRATCH/current.tfrc
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$current_rc
}

run_replacement() {
  name=$1 fixture=$2 address=$3 dependency=$4 minimum_cli=$5 dependency_check=$6
  [ "$dependency_check" = true ] || fail 'replacement dependency_check was not consumed'
  if [ "$minimum_cli" = 1.11.0 ] && [ "$CLI_SUPPORTS_111" -ne 1 ]; then
    record "replacement:$name" replacement skipped cli-version-below-1.11
    return
  fi
  assemble_matrix_fixture "$fixture" || fail 'replacement fixture assembly failed'
  namespace_workspace "$WORKSPACE"
  CLEANUP_MODE=owned
  cat >"$WORKSPACE/matrix-output.tf" <<EOF
output "matrix_replacement_id" {
  value     = $address.id
  sensitive = true
}
output "matrix_replacement_dependency_id" {
  value     = $dependency.id
  sensitive = true
}
EOF
  (cd "$WORKSPACE" && run_cli apply -auto-approve -var=replacement_phase=before) >>"$LOG" 2>&1 || fail 'replacement baseline apply failed'
  (cd "$WORKSPACE" && run_cli output -raw matrix_replacement_id) >"$SCRATCH/replacement-before" 2>>"$LOG" || fail 'old replacement identity capture failed'
  (cd "$WORKSPACE" && run_cli output -raw matrix_replacement_dependency_id) >"$SCRATCH/dependency-before" 2>>"$LOG" || fail 'old dependency identity capture failed'
  (cd "$WORKSPACE" && run_cli plan -var=replacement_phase=after -out=replacement.tfplan) >>"$LOG" 2>&1 || fail 'intentional replacement plan failed'
  (cd "$WORKSPACE" && run_cli show -json replacement.tfplan) >"$SCRATCH/replacement-plan.json" 2>>"$LOG" || fail 'replacement plan inspection failed'
  python3 - "$SCRATCH/replacement-plan.json" "$address" "$dependency" "$name" "$SCRIPT_DIR/matrix.json" <<'PY' || fail 'ordered target-only replacement and dependency relation contract failed'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8")); address=sys.argv[2]; dependency=sys.argv[3]; name=sys.argv[4]
matrix=json.load(open(sys.argv[5],encoding="utf-8"))
scenario=next(item for item in matrix["replacement_scenarios"] if item["name"]==name)
changes=[c for c in value.get("resource_changes",[]) if c.get("address")==address]
if len(changes)!=1 or changes[0].get("change",{}).get("actions",[]) != scenario["expected_actions"]:
  actual=changes[0].get("change",{}).get("actions",[]) if changes else []
  raise SystemExit("replacement actions differed: "+name+":"+",".join(actual))
for change in value.get("resource_changes",[]):
  if change.get("address") == address: continue
  if change.get("change",{}).get("actions",[]) not in ([],["no-op"]):
    raise SystemExit("replacement dependency changed: "+change.get("type","")+":"+",".join(change.get("change",{}).get("actions",[])))
# Consume the checked dependency contract and prove the plan still carries an
# explicit expression/ordering relation in either direction.
resources=value.get("configuration",{}).get("root_module",{}).get("resources",[])
def refs(item):
  found=[]
  def walk(v):
    if isinstance(v,dict):
      found.extend(v.get("references",[]) if isinstance(v.get("references"),list) else [])
      for child in v.values(): walk(child)
    elif isinstance(v,list):
      for child in v: walk(child)
  walk(item.get("expressions",{})); found.extend(item.get("depends_on",[]) or []); return found
selected={item.get("address"):refs(item) for item in resources if item.get("address") in {address,dependency}}
if set(selected)!={address,dependency}: raise SystemExit("replacement dependency address missing")
if not any(any(ref==other or ref.startswith(other+".") for ref in selected.get(owner,[])) for owner,other in ((address,dependency),(dependency,address))):
  raise SystemExit("replacement dependency relation was not preserved")
PY
  (cd "$WORKSPACE" && run_cli apply -json -auto-approve replacement.tfplan) >"$SCRATCH/replacement-apply.jsonl" 2>>"$LOG" || fail 'intentional replacement apply failed'
  python3 - "$SCRATCH/replacement-apply.jsonl" "$address" <<'PY' || fail 'replacement operation order differed from the reviewed safety order'
import json,sys
ordered=[]
for line in open(sys.argv[1],encoding="utf-8",errors="replace"):
  try: value=json.loads(line)
  except json.JSONDecodeError: continue
  hook=value.get("hook",{})
  resource=hook.get("resource",{}) if isinstance(hook,dict) else {}
  if value.get("type")=="apply_start" and resource.get("addr")==sys.argv[2]: ordered.append(hook.get("action"))
if ordered != ["create","delete"]: raise SystemExit(1)
PY
  (cd "$WORKSPACE" && run_cli output -raw matrix_replacement_id) >"$SCRATCH/replacement-after" 2>>"$LOG" || fail 'new replacement identity capture failed'
  (cd "$WORKSPACE" && run_cli output -raw matrix_replacement_dependency_id) >"$SCRATCH/dependency-after" 2>>"$LOG" || fail 'new dependency identity capture failed'
  python3 - "$SCRATCH/replacement-before" "$SCRATCH/replacement-after" "$SCRATCH/dependency-before" "$SCRATCH/dependency-after" <<'PY' || fail 'target/dependency HMAC identity contract failed'
import hashlib,hmac,secrets,sys
key=secrets.token_bytes(32)
def fp(path): return hmac.new(key,open(path,"rb").read().strip(),hashlib.sha256).digest()
if hmac.compare_digest(fp(sys.argv[1]),fp(sys.argv[2])): raise SystemExit(1)
if not hmac.compare_digest(fp(sys.argv[3]),fp(sys.argv[4])): raise SystemExit(1)
PY
  (cd "$WORKSPACE" && run_cli state show "$address") >"$SCRATCH/replacement-state" 2>>"$LOG" || fail 'stable replacement address was lost'
  set +e
  (cd "$WORKSPACE" && run_cli plan -detailed-exitcode -var=replacement_phase=after -out=post-replacement.tfplan) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  if [ "$plan_status" -eq 2 ]; then
    (cd "$WORKSPACE" && run_cli show -json post-replacement.tfplan) >"$SCRATCH/post-replacement.json" 2>>"$LOG" || fail 'post-replacement drift inspection failed'
    python3 - "$SCRATCH/post-replacement.json" <<'PY'
import json,sys
for resource in json.load(open(sys.argv[1],encoding="utf-8")).get("resource_changes",[]):
  change=resource.get("change",{}); before=change.get("before") or {}; after=change.get("after") or {}
  fields=sorted(field for field in set(before)|set(after) if before.get(field)!=after.get(field))
  if fields: print("post-replacement drift: "+resource.get("type","")+":"+",".join(fields),file=sys.stderr)
PY
  fi
  [ "$plan_status" -eq 0 ] || fail 'post-replacement plan contains drift'
  (cd "$WORKSPACE" && run_cli destroy -auto-approve -var=replacement_phase=after) >>"$LOG" 2>&1 || fail 'replacement cleanup failed'
  [ -z "$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG")" ] || fail 'replacement state was not empty after destroy'
  set +e
  (cd "$WORKSPACE" && run_cli import "$address" "$(cat "$SCRATCH/replacement-after")") >"$SCRATCH/replacement-absence.out" 2>&1
  absence_status=$?
  set -e
  cat "$SCRATCH/replacement-absence.out" >>"$LOG"
  [ "$absence_status" -ne 0 ] || fail 'replacement target remained authoritative after destroy'
  assert_authoritative_not_found "$SCRATCH/replacement-absence.out" "${address%%.*}"
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  SCENARIO_EVIDENCE=$SCRATCH/replacement-plan.json
  record "replacement:$name" replacement passed ''
}

run_failure_recovery() {
  name=$1 fixture=$2 expected_title=$3 expected_code=$4 endpoint=$5 dependency=$6 target=$7
  assemble_matrix_fixture "$fixture" || fail 'failure-recovery fixture assembly failed'
  namespace_workspace "$WORKSPACE"
  CLEANUP_MODE=owned
  cat >"$WORKSPACE/matrix-output.tf" <<EOF
output "matrix_recovery_id" {
  value     = $target.id
  sensitive = true
}
EOF
  if [ "$dependency" != - ]; then
    (cd "$WORKSPACE" && run_cli apply -auto-approve -target="$dependency" -var=recover_create=true) >>"$LOG" 2>&1 || fail 'failure-recovery dependency setup failed'
    (cd "$WORKSPACE" && run_cli state show "$dependency") >"$SCRATCH/recovery-dependency-state" 2>>"$LOG" || fail 'failure-recovery dependency was not established'
  fi
  port_file=$SCRATCH/fault-port.json
  stats_file=$SCRATCH/fault-stats.json
  python3 "$SCRIPT_DIR/fault_proxy.py" --endpoint "$endpoint" --port-file "$port_file" --stats-file "$stats_file" >>"$LOG" 2>&1 &
  PROXY_PID=$!
  attempt=0
  while [ ! -s "$port_file" ] && [ "$attempt" -lt 50 ]; do sleep 0.1; attempt=$((attempt + 1)); done
  [ -s "$port_file" ] || fail 'controlled fault proxy did not become ready'
  proxy_base=$(python3 - "$port_file" <<'PYPORT'
import json,sys
port=json.load(open(sys.argv[1],encoding="utf-8"))["port"]
if not isinstance(port,int) or not 1024 <= port <= 65535: raise SystemExit(1)
print(f"http://127.0.0.1:{port}")
PYPORT
) || fail 'controlled fault proxy port was invalid'
  set +e
  (cd "$WORKSPACE" && run_cli apply -refresh=false -json -auto-approve -var=recover_create=true -var="litellm_api_base=$proxy_base") >"$SCRATCH/failure.jsonl" 2>>"$LOG"
  failed_status=$?
  set -e
  [ "$failed_status" -ne 0 ] || fail 'controlled pre-commit fault unexpectedly succeeded'
  python3 - "$SCRATCH/failure.jsonl" "$name" "$expected_title" "$expected_code" <<'PYDIAG' || fail 'failure diagnostic did not map to the exact scenario-specific title/code'
import json,sys
allowed={
 "model_failed_create_retry":("Client Error","model-create-error"),
 "team_failed_create_retry":("Client Error","team-create-error"),
}
name,expected,code=sys.argv[2:]
if allowed.get(name)!=(expected,code): raise SystemExit(1)
titles=[]
for line in open(sys.argv[1],encoding="utf-8",errors="replace"):
  try: value=json.loads(line)
  except json.JSONDecodeError: continue
  diagnostic=value.get("diagnostic",{})
  if isinstance(diagnostic,dict) and diagnostic.get("severity")=="error" and isinstance(diagnostic.get("summary"),str): titles.append(diagnostic["summary"])
if sorted(set(titles)) != [expected]: raise SystemExit("unexpected error diagnostics: "+",".join(sorted(set(titles))))
PYDIAG
  [ -s "$stats_file" ] || fail 'controlled endpoint was not attempted'
  python3 - "$stats_file" <<'PYSTATS' || fail 'fault proxy did not prove a pre-commit target attempt'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8"))
if value.get("attempted") != 1 or value.get("faulted_before_forward") != 1 or value.get("target_forwarded") != 0: raise SystemExit(1)
if not isinstance(value.get("other_forwarded"),int) or value["other_forwarded"] < 0: raise SystemExit(1)
PYSTATS
  if (cd "$WORKSPACE" && run_cli state list 2>>"$LOG" | grep -Fx "$target" >/dev/null); then
    fail 'failed pre-commit target leaked identity/private state'
  fi
  kill "$PROXY_PID" 2>/dev/null || true
  wait "$PROXY_PID" 2>/dev/null || true
  PROXY_PID=
  (cd "$WORKSPACE" && run_cli apply -auto-approve -var=recover_create=true) >>"$LOG" 2>&1 || fail 'controlled-fault recovery apply failed'
  (cd "$WORKSPACE" && run_cli output -raw matrix_recovery_id) >"$SCRATCH/recovery-id" 2>>"$LOG" || fail 'recovery identity capture failed'
  set +e
  (cd "$WORKSPACE" && run_cli plan -detailed-exitcode -var=recover_create=true) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'recovered create contains drift'
  (cd "$WORKSPACE" && run_cli destroy -auto-approve -var=recover_create=true) >>"$LOG" 2>&1 || fail 'failure-recovery cleanup failed'
  [ -z "$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG")" ] || fail 'recovery state was not empty after cleanup'
  set +e
  (cd "$WORKSPACE" && run_cli import "$target" "$(cat "$SCRATCH/recovery-id")") >"$SCRATCH/recovery-absence.out" 2>&1
  absence_status=$?
  set -e
  cat "$SCRATCH/recovery-absence.out" >>"$LOG"
  [ "$absence_status" -ne 0 ] || fail 'recovery target remained authoritative after cleanup'
  assert_authoritative_not_found "$SCRATCH/recovery-absence.out" "${target%%.*}"
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  SCENARIO_EVIDENCE=$stats_file
  record "failure_recovery:$name" failure_recovery passed '' "$expected_title"
}

# Remote execution is intentionally assembly/read-only in this change. The
# separate confirmation and unique namespace were validated above, but no API
# request or mutation is made until the manual scenario allowlist is enabled.
if [ "$MODE" = remote ]; then
  python3 "$SCRIPT_DIR/harness.py" assembly --cli "$CLI" --provider-binary "$PROVIDER_BINARY" --report "$REPORT"
  printf '%s\n' 'Remote safety preflight and assembly passed; mutation remains disabled'
  exit 0
fi

PHASE=${MATRIX_PHASE:-all}
[ "$PHASE" = all ] || [ "$PHASE" = upgrade ] || [ "$PHASE" = import ] || [ "$PHASE" = scenarios ] || fail 'MATRIX_PHASE must be all, upgrade, import, or scenarios'

if [ "$PHASE" = all ]; then
# Re-run the complete non-mutating assembly against the exact selected CLI and
# current binary. Only after that command succeeds can the three reviewed
# documentation contracts receive execution records.
run_bounded python3 "$SCRIPT_DIR/harness.py" assembly --cli "$CLI" --provider-binary "$PROVIDER_BINARY" >>"$LOG" 2>&1 || fail 'local documentation/assembly contracts failed'
python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/documentation.tsv"
import json,sys
for name in json.load(open(sys.argv[1],encoding="utf-8"))["documentation_scenarios"]: print(name)
PY
while IFS= read -r documentation_scenario; do
  record "documentation:$documentation_scenario" documentation passed ''
done <"$SCRATCH/documentation.tsv"
# Existing lifecycle matrix covers all OSS resources and data sources. Route
# its historical `terraform` command name to the selected Terraform/OpenTofu
# binary so the protocol matrix cannot silently fall back to PATH Terraform.
selected_cli=$(command -v "$CLI") || fail 'selected CLI is not executable'
MATRIX_EXECUTED_CLI=$selected_cli
export MATRIX_EXECUTED_CLI
mkdir -m 700 "$SCRATCH/cli-bin"
cat >"$SCRATCH/cli-bin/terraform" <<EOF
#!/bin/sh
exec python3 $(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$SCRIPT_DIR/deadline.py") --seconds $COMMAND_TIMEOUT $(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$selected_cli") "\$@"
EOF
chmod 700 "$SCRATCH/cli-bin/terraform"
PATH="$SCRATCH/cli-bin:$PATH"
export PATH
PROVIDER_DIR=$(dirname "$PROVIDER_BINARY")
SMOKE_PRIVATE_ROOT=$SCRATCH/smoke
SMOKE_DELETE_LOGS=1
mkdir -m 700 "$SMOKE_PRIVATE_ROOT"
export PROVIDER_DIR SMOKE_PRIVATE_ROOT SMOKE_DELETE_LOGS
LITELLM_ACCEPTANCE_ASSEMBLY_ONLY=0 sh "$REPO_ROOT/internal_testing/acceptance.sh" >>"$LOG" 2>&1 || fail 'lifecycle/data-source matrix failed'
# Project is the only registered resource and pair of data sources unavailable
# in the pinned OSS edition. These are explicit execution records, never passes.
record 'resource_coverage:litellm_project' resource_coverage skipped enterprise-license-required
record 'lifecycle:litellm_project' lifecycle skipped enterprise-license-required
record 'drift:litellm_project' drift skipped enterprise-license-required
record 'data_source:litellm_project' data_source skipped enterprise-license-required
record 'data_source:litellm_projects' data_source skipped enterprise-license-required
fi

# Parse controlled TSV fields from the checked-in manifest.
python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/resources.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["resources"]:
 print("\t".join([r["type"], ",".join(r["fixture"]), r["address"], r["import_expression"], r["lane"], str(r.get("introduced_after_previous",False)).lower()]))
PY
if [ "$PHASE" = all ] || [ "$PHASE" = upgrade ]; then
  while IFS="	" read -r resource_type fixtures address expression lane introduced_after_previous; do
    run_upgrade "$resource_type" "$fixtures" "$lane" "$introduced_after_previous"
  done <"$SCRATCH/resources.tsv"
fi
if [ "$PHASE" = upgrade ]; then
  printf '%s\n' 'Upgrade-only diagnostic phase completed; no report was written'
  exit 0
fi
if [ "$PHASE" != scenarios ]; then
python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/imports.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["resources"]:
 print("\t".join([r["type"], ",".join(r["fixture"]), ",".join(r.get("import_fixture",r["fixture"])), r["address"], r["import_expression"], r["lane"], r.get("import_skip_reason", "-")]))
PY
while IFS="	" read -r resource_type producer_fixtures importer_fixtures address expression lane expected_skip; do
  [ "$expected_skip" = - ] && expected_skip=
  run_import "$resource_type" "$producer_fixtures" "$importer_fixtures" "$address" "$expression" "$lane" "$expected_skip"
done <"$SCRATCH/imports.tsv"
fi
if [ "$PHASE" = import ]; then
  printf '%s\n' 'Import-only diagnostic phase completed; no report was written'
  exit 0
fi

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/replacements.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["replacement_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["address"],r["dependency_address"],r.get("minimum_cli","-"),str(r.get("dependency_check",False)).lower()]))
PY
while IFS="	" read -r scenario_name fixture address dependency minimum_cli dependency_check; do
  run_replacement "$scenario_name" "$fixture" "$address" "$dependency" "$minimum_cli" "$dependency_check"
done <"$SCRATCH/replacements.tsv"

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/recovery.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["failure_recovery_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["expected_diagnostic_title"],r["expected_diagnostic_code"],r["fault_endpoint"],r["dependency_address"] or "-",r["target_address"]]))
PY
while IFS="	" read -r scenario_name fixture expected_title expected_code endpoint dependency target; do
  run_failure_recovery "$scenario_name" "$fixture" "$expected_title" "$expected_code" "$endpoint" "$dependency" "$target"
done <"$SCRATCH/recovery.tsv"
if [ "$PHASE" = scenarios ]; then
  printf '%s\n' 'Replacement/recovery diagnostic phase completed; no report was written'
  exit 0
fi

python3 "$SCRIPT_DIR/harness.py" finalize-evidence \
  --session "$SESSION" --report "$REPORT" --evidence-report "$EVIDENCE_REPORT" \
  --cli "$CLI" --previous-provider-binary "$PREVIOUS_PROVIDER_BINARY"
printf '%s\n' 'Matrix passed; report and safe ledger derive only from trusted bounded execution evidence'
