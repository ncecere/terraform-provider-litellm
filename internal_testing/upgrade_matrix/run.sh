#!/bin/sh
# Upgrade/import matrix entrypoint. Child output is captured in private scratch
# space and is never copied to artifacts or stdout.
set -eu
umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
MODE=${1:-assembly}
CLI=${MATRIX_CLI:-terraform}
REPORT=${MATRIX_REPORT:-$REPO_ROOT/internal_testing/upgrade-matrix-results.json}
PROVIDER_BINARY=${MATRIX_PROVIDER_BINARY:-$REPO_ROOT/terraform-provider-litellm}
CACHE=${MATRIX_CACHE:-${HOME}/.cache/terraform-provider-litellm}
SCRATCH=
LOG=
CLEANUP_MODE=none
WORKSPACE=

fail() {
  printf '%s\n' "Matrix failed: $1" >&2
  exit 1
}

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  cleanup_status=0
  if [ -n "$WORKSPACE" ] && [ -d "$WORKSPACE" ]; then
    if [ "$CLEANUP_MODE" = import ]; then
      # Imported/pre-existing objects are detached only. Never substitute destroy.
      addresses=$(cd "$WORKSPACE" && "$CLI" state list 2>>"$LOG" || cleanup_status=$?)
      if [ "$cleanup_status" -eq 0 ]; then
        for address in $addresses; do
          (cd "$WORKSPACE" && "$CLI" state rm "$address") >>"$LOG" 2>&1 || cleanup_status=$?
        done
      fi
    elif [ "$CLEANUP_MODE" = owned ]; then
      (cd "$WORKSPACE" && "$CLI" destroy -refresh=false -auto-approve) >>"$LOG" 2>&1 || cleanup_status=$?
    fi
  fi
  [ -z "$SCRATCH" ] || rm -rf "$SCRATCH"
  if [ "$cleanup_status" -ne 0 ]; then
    printf '%s\n' 'Matrix failed: cleanup did not complete' >&2
    exit 1
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

SCRATCH=$(mktemp -d "${TMPDIR:-/tmp}/litellm-issue210.XXXXXX")
chmod 700 "$SCRATCH"
LOG=$SCRATCH/matrix.log
: >"$LOG"
chmod 600 "$LOG"
# Bound every redirected child log written by this shell (10 MiB on POSIX
# implementations whose file-size limit unit is 512-byte blocks).
ulimit -f 20480 2>/dev/null || fail 'could not establish the private log size limit'
RESULTS=$SCRATCH/results.tsv
: >"$RESULTS"

# Ambient logging/CLI arguments can expose bodies or redirect the backend.
unset TF_LOG TF_LOG_PATH TF_CLI_ARGS TF_CLI_ARGS_init TF_CLI_ARGS_plan TF_CLI_ARGS_apply TF_CLI_ARGS_destroy
export TF_IN_AUTOMATION=1 TF_INPUT=0 TF_CLI_ARGS=-no-color

if [ "$MODE" = local ]; then
  [ "${LITELLM_API_BASE:-http://localhost:4000}" = 'http://localhost:4000' ] || fail 'local target must be loopback port 4000'
  [ "${LITELLM_API_KEY:-sk-testing-key}" = 'sk-testing-key' ] || fail 'local target must use the disposable stack credential'
  grep -q 'docker.litellm.ai/berriai/litellm:v1.98.0' "$REPO_ROOT/internal_testing/docker-compose.yml" || fail 'local image is not the exact pinned release'
  version=$(curl --fail --silent --show-error 'http://localhost:4000/openapi.json' 2>>"$LOG" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("info",{}).get("version",""))')
  [ "$version" = '1.98.0' ] || fail 'local LiteLLM version check failed'
  export TF_VAR_litellm_api_base='http://localhost:4000'
  export TF_VAR_litellm_api_key='sk-testing-key'
else
  [ "${LITELLM_API_BASE:-}" = 'https://dev.api.ai.it.ufl.edu' ] || fail 'remote target does not match the approved development deployment'
  [ -n "${LITELLM_API_KEY:-}" ] || fail 'remote credential is required'
  export TF_VAR_litellm_api_base=$LITELLM_API_BASE
  export TF_VAR_litellm_api_key=$LITELLM_API_KEY
fi

if [ "${MATRIX_OFFLINE:-0}" = 1 ]; then
  python3 "$SCRIPT_DIR/harness.py" install-previous --cache "$CACHE/providers" --offline >>"$LOG" 2>&1
else
  python3 "$SCRIPT_DIR/harness.py" install-previous --cache "$CACHE/providers" >>"$LOG" 2>&1
fi
PREVIOUS_MIRROR=$CACHE/providers/mirror

write_provider_config() {
  destination=$1
  cat >"$destination/provider.tf" <<'TF'
terraform {
  required_version = ">= 1.0.0"
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
  direct {}
}
EOF
}

record() {
  # name/category/status/reason are controlled matrix labels, never remote data.
  log_size=$(wc -c <"$LOG")
  [ "$log_size" -le 10485760 ] || fail 'private child log exceeded its bounded size'
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "${5:-}" >>"$RESULTS"
  printf '%-18s %-38s %s\n' "$2" "$1" "$3"
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
  "$CLI" fmt -check "$WORKSPACE" >>"$LOG" 2>&1 || return 1
}

state_contract() {
  state_file=$1
  output=$2
  python3 - "$state_file" "$output" <<'PY'
import hashlib, hmac, json, os, sys
value=json.load(open(sys.argv[1], encoding="utf-8"))
rows=[]
def walk(module):
    for resource in module.get("resources", []):
        if resource.get("mode") == "managed":
            values=resource.get("values", {})
            identifier=values.get("id")
            fingerprint=hmac.new(os.environ["MATRIX_HMAC_KEY"].encode(), str(identifier).encode(), hashlib.sha256).hexdigest() if identifier is not None else None
            rows.append([resource.get("address"), resource.get("type"), resource.get("schema_version", 0), fingerprint])
    for child in module.get("child_modules", []): walk(child)
walk(value.get("values", {}).get("root_module", {}))
json.dump(sorted(rows), open(sys.argv[2], "w", encoding="utf-8"), separators=(",", ":"))
PY
}

run_upgrade() {
  resource_type=$1 fixtures=$2 lane=$3
  if [ "$lane" = enterprise ] && [ "${LITELLM_ENTERPRISE_CONFIRM:-}" != licensed-disposable ]; then
    record "upgrade:$resource_type" upgrade skipped enterprise-license-required
    return
  fi
  assemble_workspace "$resource_type" "$fixtures" || fail 'upgrade fixture assembly failed'
  CLEANUP_MODE=owned
  old_rc=$SCRATCH/old.tfrc
  current_rc=$SCRATCH/current.tfrc
  write_old_config "$old_rc"
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$old_rc
  (cd "$WORKSPACE" && "$CLI" init -backend=false -lockfile=readonly) >>"$LOG" 2>&1 || {
    # The first verified init creates the lock file; retry without readonly only for that operation.
    (cd "$WORKSPACE" && "$CLI" init -backend=false) >>"$LOG" 2>&1 || fail 'published-provider initialization failed'
  }
  grep -q 'version *= *"2.0.1"' "$WORKSPACE/.terraform.lock.hcl" || fail 'lock file did not select published 2.0.1'
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve) >>"$LOG" 2>&1 || fail 'previous-release baseline apply failed'
  (cd "$WORKSPACE" && "$CLI" show -json) >"$SCRATCH/state-before.json" 2>>"$LOG" || fail 'baseline state inspection failed'
  MATRIX_HMAC_KEY=$(python3 -c 'import secrets; print(secrets.token_hex(32))')
  export MATRIX_HMAC_KEY
  state_contract "$SCRATCH/state-before.json" "$SCRATCH/contract-before.json"
  export TF_CLI_CONFIG_FILE=$current_rc
  set +e
  (cd "$WORKSPACE" && "$CLI" plan -detailed-exitcode -refresh=true) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'current provider proposed drift or replacement for previous-release state'
  (cd "$WORKSPACE" && "$CLI" apply -refresh-only -auto-approve) >>"$LOG" 2>&1 || fail 'current-provider refresh-only apply failed'
  (cd "$WORKSPACE" && "$CLI" show -json) >"$SCRATCH/state-after.json" 2>>"$LOG" || fail 'upgraded state inspection failed'
  state_contract "$SCRATCH/state-after.json" "$SCRATCH/contract-after.json"
  cmp -s "$SCRATCH/contract-before.json" "$SCRATCH/contract-after.json" || fail 'address, type, schema, or private ID changed during upgrade'
  (cd "$WORKSPACE" && "$CLI" destroy -auto-approve) >>"$LOG" 2>&1 || fail 'owned upgrade fixture cleanup failed'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "upgrade:$resource_type" upgrade passed ''
}

run_import() {
  resource_type=$1 fixtures=$2 address=$3 expression=$4 lane=$5
  if [ "$lane" = enterprise ] && [ "${LITELLM_ENTERPRISE_CONFIRM:-}" != licensed-disposable ]; then
    record "import:$resource_type" import skipped enterprise-license-required
    return
  fi
  [ "$MODE" = local ] || {
    record "import:$resource_type" import skipped inventory-endpoint-may-be-unavailable
    return
  }
  assemble_workspace "$resource_type" "$fixtures" || fail 'import fixture assembly failed'
  CLEANUP_MODE=import
  current_rc=$SCRATCH/current.tfrc
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$current_rc
  if echo "$expression" | grep -q '^composite_team_member_add'; then
    : # Derived below from state without displaying its components.
  else
    cat >"$WORKSPACE/matrix-output.tf" <<EOF
output "matrix_import_id" {
  value     = $expression
  sensitive = true
}
EOF
  fi
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve) >>"$LOG" 2>&1 || fail 'disposable import seed apply failed'
  if echo "$expression" | grep -q '^composite_team_member_add'; then
    (cd "$WORKSPACE" && "$CLI" show -json) >"$SCRATCH/import-state.json" 2>>"$LOG" || fail 'import identity inspection failed'
    python3 - "$SCRATCH/import-state.json" "$address" >"$SCRATCH/import-id" <<'PY'
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
PY
  else
    (cd "$WORKSPACE" && "$CLI" output -raw matrix_import_id) >"$SCRATCH/import-id" 2>>"$LOG" || fail 'import identity capture failed'
  fi
  [ -s "$SCRATCH/import-id" ] || fail 'import identity was empty'
  (cd "$WORKSPACE" && "$CLI" state rm "$address") >>"$LOG" 2>&1 || fail 'pre-import state detach failed'
  (cd "$WORKSPACE" && "$CLI" import "$address" "$(cat "$SCRATCH/import-id")") >>"$LOG" 2>&1 || fail 'import failed'
  (cd "$WORKSPACE" && "$CLI" refresh) >>"$LOG" 2>&1 || fail 'post-import refresh failed'
  set +e
  (cd "$WORKSPACE" && "$CLI" plan -detailed-exitcode) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'post-import plan contains drift'
  # Never destroy imported or dependency objects; detach all state entries.
  addresses=$(cd "$WORKSPACE" && "$CLI" state list 2>>"$LOG") || fail 'import cleanup inventory failed'
  for item in $addresses; do
    (cd "$WORKSPACE" && "$CLI" state rm "$item") >>"$LOG" 2>&1 || fail 'state-rm import cleanup failed'
  done
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "import:$resource_type" import passed ''
}

assemble_matrix_fixture() {
  fixture=$1
  WORKSPACE=$SCRATCH/work
  rm -rf "$WORKSPACE"
  mkdir -m 700 "$WORKSPACE"
  write_provider_config "$WORKSPACE"
  cp "$SCRIPT_DIR/$fixture" "$WORKSPACE/scenario.tf"
  "$CLI" fmt -check "$WORKSPACE" >>"$LOG" 2>&1 || return 1
  current_rc=$SCRATCH/current.tfrc
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$current_rc
}

run_replacement() {
  name=$1 fixture=$2 address=$3
  assemble_matrix_fixture "$fixture" || fail 'replacement fixture assembly failed'
  CLEANUP_MODE=owned
  cat >"$WORKSPACE/matrix-output.tf" <<EOF
output "matrix_replacement_id" {
  value     = $address.id
  sensitive = true
}
EOF
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve -var=replacement_phase=before) >>"$LOG" 2>&1 || fail 'replacement baseline apply failed'
  (cd "$WORKSPACE" && "$CLI" output -raw matrix_replacement_id) >"$SCRATCH/replacement-before" 2>>"$LOG" || fail 'old replacement identity capture failed'
  (cd "$WORKSPACE" && "$CLI" plan -var=replacement_phase=after -out=replacement.tfplan) >>"$LOG" 2>&1 || fail 'intentional replacement plan failed'
  (cd "$WORKSPACE" && "$CLI" show -json replacement.tfplan) >"$SCRATCH/replacement-plan.json" 2>>"$LOG" || fail 'replacement plan inspection failed'
  python3 - "$SCRATCH/replacement-plan.json" "$address" <<'PY' || fail 'plan did not contain the required replacement'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8")); address=sys.argv[2]
changes=[c for c in value.get("resource_changes",[]) if c.get("address")==address]
if len(changes)!=1 or set(changes[0].get("change",{}).get("actions",[]))!={"create","delete"}: raise SystemExit(1)
PY
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve replacement.tfplan) >>"$LOG" 2>&1 || fail 'intentional replacement apply failed'
  (cd "$WORKSPACE" && "$CLI" output -raw matrix_replacement_id) >"$SCRATCH/replacement-after" 2>>"$LOG" || fail 'new replacement identity capture failed'
  python3 - "$SCRATCH/replacement-before" "$SCRATCH/replacement-after" <<'PY' || fail 'replacement retained the old remote identity'
import hashlib,hmac,secrets,sys
key=secrets.token_bytes(32)
def fp(path): return hmac.new(key,open(path,"rb").read().strip(),hashlib.sha256).digest()
if hmac.compare_digest(fp(sys.argv[1]),fp(sys.argv[2])): raise SystemExit(1)
PY
  (cd "$WORKSPACE" && "$CLI" state show "$address") >"$SCRATCH/replacement-state" 2>>"$LOG" || fail 'stable replacement address was lost'
  set +e
  (cd "$WORKSPACE" && "$CLI" plan -detailed-exitcode -var=replacement_phase=after) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'post-replacement plan contains drift'
  (cd "$WORKSPACE" && "$CLI" destroy -auto-approve -var=replacement_phase=after) >>"$LOG" 2>&1 || fail 'replacement cleanup failed'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "replacement:$name" replacement passed ''
}

run_failure_recovery() {
  name=$1 fixture=$2 expected_title=$3
  assemble_matrix_fixture "$fixture" || fail 'failure-recovery fixture assembly failed'
  CLEANUP_MODE=owned
  set +e
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve -var=recover_create=true -var=litellm_api_base=http://127.0.0.1:1) >>"$LOG" 2>&1
  failed_status=$?
  set -e
  [ "$failed_status" -ne 0 ] || fail 'failed-create injection unexpectedly succeeded'
  (cd "$WORKSPACE" && "$CLI" apply -auto-approve -var=recover_create=true) >>"$LOG" 2>&1 || fail 'failed-create recovery apply failed'
  set +e
  (cd "$WORKSPACE" && "$CLI" plan -detailed-exitcode -var=recover_create=true) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'recovered create contains drift'
  (cd "$WORKSPACE" && "$CLI" destroy -auto-approve -var=recover_create=true) >>"$LOG" 2>&1 || fail 'failure-recovery cleanup failed'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "failure-recovery:$name" failure_recovery passed '' "$expected_title"
}

# Remote execution is intentionally assembly/read-only in this change. The
# separate confirmation and unique namespace were validated above, but no API
# request or mutation is made until the manual scenario allowlist is enabled.
if [ "$MODE" = remote ]; then
  python3 "$SCRIPT_DIR/harness.py" assembly --cli "$CLI" --provider-binary "$PROVIDER_BINARY" --report "$REPORT"
  printf '%s\n' 'Remote safety preflight and assembly passed; mutation remains disabled'
  exit 0
fi

# Existing lifecycle matrix covers all OSS resources and data sources. Route
# its historical `terraform` command name to the selected Terraform/OpenTofu
# binary so the protocol matrix cannot silently fall back to PATH Terraform.
selected_cli=$(command -v "$CLI") || fail 'selected CLI is not executable'
mkdir -m 700 "$SCRATCH/cli-bin"
ln -s "$selected_cli" "$SCRATCH/cli-bin/terraform"
PATH="$SCRATCH/cli-bin:$PATH"
export PATH
LITELLM_ACCEPTANCE_ASSEMBLY_ONLY=0 sh "$REPO_ROOT/internal_testing/acceptance.sh" >>"$LOG" 2>&1 || fail 'lifecycle/data-source matrix failed'
record lifecycle-matrix lifecycle passed ''

# Parse controlled TSV fields from the checked-in manifest.
python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/resources.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["resources"]:
 print("\t".join([r["type"], ",".join(r["fixture"]), r["address"], r["import_expression"], r["lane"]]))
PY
while IFS="	" read -r resource_type fixtures address expression lane; do
  run_upgrade "$resource_type" "$fixtures" "$lane"
done <"$SCRATCH/resources.tsv"
while IFS="	" read -r resource_type fixtures address expression lane; do
  run_import "$resource_type" "$fixtures" "$address" "$expression" "$lane"
done <"$SCRATCH/resources.tsv"

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/replacements.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["replacement_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["address"]]))
PY
while IFS="	" read -r scenario_name fixture address; do
  run_replacement "$scenario_name" "$fixture" "$address"
done <"$SCRATCH/replacements.tsv"

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/recovery.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["failure_recovery_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["expected_diagnostic_title"]]))
PY
while IFS="	" read -r scenario_name fixture expected_title; do
  run_failure_recovery "$scenario_name" "$fixture" "$expected_title"
done <"$SCRATCH/recovery.tsv"

python3 - "$RESULTS" "$REPORT" "$SCRIPT_DIR/matrix.json" <<'PY'
import json,sys
results=[]
for line in open(sys.argv[1], encoding="utf-8"):
 name,category,status,reason,title=(line.rstrip("\n").split("\t")+[""]*5)[:5]
 item={"name":name,"category":category,"status":status,"diagnostic_titles":[title] if title else []}
 if reason: item["reason"]=reason
 results.append(item)
matrix=json.load(open(sys.argv[3], encoding="utf-8"))
allowed=set(matrix["allowed_skip_reasons"])
if any(r["status"]=="skipped" and r.get("reason") not in allowed for r in results): raise SystemExit(1)
report={"schema_version":1,"mode":"destructive-local","summary":matrix["scenario_counts"],"scenarios":results,"tools":{}}
json.dump(report,open(sys.argv[2],"w",encoding="utf-8"),indent=2,sort_keys=True); open(sys.argv[2],"a").write("\n")
PY
chmod 600 "$REPORT"
python3 - "$SCRIPT_DIR" "$REPORT" <<'PY'
import importlib.util,sys
from pathlib import Path
spec=importlib.util.spec_from_file_location("h",Path(sys.argv[1])/"harness.py"); h=importlib.util.module_from_spec(spec); spec.loader.exec_module(h)
h.credential_scan([Path(sys.argv[2])])
PY
printf '%s\n' 'Matrix passed; machine-readable summary written without remote values'
