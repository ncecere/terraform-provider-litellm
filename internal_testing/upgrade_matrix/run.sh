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
PROXY_PID=
COMMAND_TIMEOUT=${MATRIX_COMMAND_TIMEOUT:-300}
case $COMMAND_TIMEOUT in ''|*[!0-9]*) printf '%s\n' 'Matrix failed: invalid command timeout' >&2; exit 1 ;; esac
[ "$COMMAND_TIMEOUT" -ge 1 ] && [ "$COMMAND_TIMEOUT" -le 900 ] || { printf '%s\n' 'Matrix failed: command timeout is out of bounds' >&2; exit 1; }
run_cli() {
  python3 "$SCRIPT_DIR/deadline.py" --seconds "$COMMAND_TIMEOUT" "$CLI" "$@"
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
  if [ -n "$WORKSPACE" ] && [ -d "$WORKSPACE" ]; then
    if [ "$CLEANUP_MODE" = import ]; then
      # Imported/pre-existing objects are detached only. Never substitute destroy.
      addresses=$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG" || cleanup_status=$?)
      if [ "$cleanup_status" -eq 0 ]; then
        for address in $addresses; do
          (cd "$WORKSPACE" && run_cli state rm "$address") >>"$LOG" 2>&1 || cleanup_status=$?
        done
      fi
    elif [ "$CLEANUP_MODE" = owned ]; then
      (cd "$WORKSPACE" && run_cli destroy -refresh=false -auto-approve) >>"$LOG" 2>&1 || cleanup_status=$?
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
SOURCE_PROVIDER_BINARY=$PROVIDER_BINARY

TMP_BASE=$(python3 -c 'from pathlib import Path; import os; print(Path(os.environ.get("TMPDIR", "/tmp")).resolve())')
SCRATCH=$(mktemp -d "$TMP_BASE/litellm-issue210.XXXXXX")
chmod 700 "$SCRATCH"
provider_dir=$(python3 "$SCRIPT_DIR/harness.py" prepare-provider \
  --provider-binary "$SOURCE_PROVIDER_BINARY" --directory "$SCRATCH/provider-cache") || fail 'provider private bundle verification failed'
PROVIDER_BINARY=$provider_dir/terraform-provider-litellm
[ -x "$PROVIDER_BINARY" ] || fail 'verified provider executable is unavailable'
LOG=$SCRATCH/matrix.log
: >"$LOG"
chmod 600 "$LOG"
# Bound every redirected child log written by this shell (10 MiB on POSIX
# implementations whose file-size limit unit is 512-byte blocks).
ulimit -f 20480 2>/dev/null || fail 'could not establish the private log size limit'
RESULTS=$SCRATCH/results.tsv
: >"$RESULTS"
chmod 600 "$RESULTS"
export MATRIX_EXECUTION_RECORDS=$RESULTS

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
}
EOF
}

record() {
  # name/category/status/reason are controlled matrix labels, never remote data.
  log_size=$(wc -c <"$LOG")
  [ "$log_size" -le 10485760 ] || fail 'private child log exceeded its bounded size'
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "${5:-}" "${6:-}" >>"$RESULTS"
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
  run_cli fmt -check "$WORKSPACE" >>"$LOG" 2>&1 || return 1
}

compare_upgrade_states() {
  before=$1 after=$2 schema=$3 resource_type=$4 raw_before=$5 raw_after=$6
  python3 - "$before" "$after" "$schema" "$resource_type" "$raw_before" "$raw_after" "$SCRIPT_DIR/matrix.json" <<'PYUPGRADE'
import hashlib,hmac,json,secrets,sys
before,after,schema_path,wanted,raw_before,raw_after,matrix_path=sys.argv[1:]
schema=json.load(open(schema_path,encoding="utf-8"))["provider_schemas"]["registry.terraform.io/ncecere/litellm"]
rs=schema["resource_schemas"]
matrix=json.load(open(matrix_path,encoding="utf-8"))
allowed_map=matrix.get("upgrade_expected_computed_migrations",{})
private_migrations=set(matrix.get("upgrade_expected_private_migrations",[]))
schema_migrations=matrix.get("upgrade_expected_schema_migrations",{})
identity_migrations=matrix.get("upgrade_expected_identity_migrations",{})
key=secrets.token_bytes(32)
def rows(path):
  value=json.load(open(path,encoding="utf-8")); out={}
  def walk(module):
    for resource in module.get("resources",[]):
      if resource.get("mode")!="managed": continue
      address=resource["address"]; typ=resource["type"]; values=resource.get("values",{})
      attrs=rs[typ]["block"].get("attributes",{})
      # Canonicalize absent attributes to null under the current schema. This
      # treats a newly introduced optional-null field as semantic absence while
      # still comparing every non-sensitive schema field.
      clean={k:values.get(k) for k,meta in attrs.items() if not meta.get("sensitive",False)}
      out[address]={"type":typ,"schema_version":resource.get("schema_version",0),"values":clean}
    for child in module.get("child_modules",[]): walk(child)
  walk(value.get("values",{}).get("root_module",{})); return out
left,right=rows(before),rows(after)
if set(left)!=set(right): raise SystemExit("address set changed")
migrated=False
for address in left:
  if left[address]["type"]!=right[address]["type"]: raise SystemExit("type changed")
  typ=left[address]["type"]
  if left[address]["schema_version"]!=right[address]["schema_version"]:
    if [left[address]["schema_version"],right[address]["schema_version"]] != schema_migrations.get(typ): raise SystemExit("schema version changed without reviewed migration")
    migrated=True
  lv,rv=left[address]["values"],right[address]["values"]
  for field in allowed_map.get(left[address]["type"],[]):
    if lv.get(field)!=rv.get(field): migrated=True
    lv.pop(field,None); rv.pop(field,None)
  left_id,right_id=lv.pop("id",None),rv.pop("id",None)
  if identity_migrations.get(typ)=="sha256-of-prior-id":
    expected="sha256:"+hashlib.sha256(str(left_id).encode()).hexdigest()
    if not hmac.compare_digest(expected,str(right_id)): raise SystemExit("reviewed identity migration mismatch")
    migrated=True
  elif not hmac.compare_digest(hmac.new(key,str(left_id).encode(),hashlib.sha256).digest(),hmac.new(key,str(right_id).encode(),hashlib.sha256).digest()):
    raise SystemExit("resource identity changed")
  if json.dumps(lv,sort_keys=True,separators=(",",":")) != json.dumps(rv,sort_keys=True,separators=(",",":")):
    raise SystemExit("nonsecret semantic state changed")
def private_signals(path):
  value=json.load(open(path,encoding="utf-8")); signals={}
  for resource in value.get("resources",[]):
    for index,instance in enumerate(resource.get("instances",[])):
      private=instance.get("private","") or ""
      signals[(resource.get("module",""),resource.get("type"),resource.get("name"),index)]=bool(private)
  return signals
private_before,private_after=private_signals(raw_before),private_signals(raw_after)
if set(private_before)!=set(private_after): raise SystemExit("provider-private address set changed")
for identity,old_present in private_before.items():
  new_present=private_after[identity]
  if old_present==new_present: continue
  if not old_present and new_present and identity[1] in private_migrations:
    migrated=True
    continue
  raise SystemExit("provider-private presence changed without reviewed migration")
if migrated: print("upgrade-reviewed-migration")
PYUPGRADE
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
  (cd "$WORKSPACE" && run_cli init -backend=false -lockfile=readonly) >>"$LOG" 2>&1 || {
    # The first verified init creates the lock file; retry without readonly only for that operation.
    (cd "$WORKSPACE" && run_cli init -backend=false) >>"$LOG" 2>&1 || fail 'published-provider initialization failed'
  }
  grep -q 'version *= *"2.0.1"' "$WORKSPACE/.terraform.lock.hcl" || fail 'lock file did not select published 2.0.1'
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
  python3 - "$SCRATCH/current-upgrade-plan.json" "$SCRIPT_DIR/matrix.json" <<'PY' || fail 'upgrade proposed non-reviewed drift or replacement'
import json,sys
plan=json.load(open(sys.argv[1],encoding="utf-8")); matrix=json.load(open(sys.argv[2],encoding="utf-8"))
allowed=matrix.get("upgrade_expected_computed_migrations",{})
for resource in plan.get("resource_changes",[]):
  change=resource.get("change",{}); actions=change.get("actions",[])
  if actions not in ([],["no-op"]): raise SystemExit(1)
  before=change.get("before") or {}; after=change.get("after") or {}
  sensitive=(change.get("after_sensitive") or {})
  fields={key for key in set(before)|set(after) if before.get(key)!=after.get(key) and not sensitive.get(key,False)}
  if not fields.issubset(set(allowed.get(resource.get("type"),[]))): raise SystemExit(1)
PY
  (cd "$WORKSPACE" && run_cli apply -refresh-only -auto-approve) >>"$LOG" 2>&1 || fail 'current-provider refresh-only apply failed'
  (cd "$WORKSPACE" && run_cli show -json) >"$SCRATCH/state-after.json" 2>>"$LOG" || fail 'upgraded state inspection failed'
  (cd "$WORKSPACE" && run_cli state pull) >"$SCRATCH/raw-after.json" 2>>"$LOG" || fail 'upgraded private-state inspection failed'
  migration_code=$(compare_upgrade_states "$SCRATCH/state-before.json" "$SCRATCH/state-after.json" \
    "$SCRATCH/current-schema.json" "$resource_type" "$SCRATCH/raw-before.json" \
    "$SCRATCH/raw-after.json") || fail 'canonical upgrade state contract failed'
  (cd "$WORKSPACE" && run_cli destroy -auto-approve) >>"$LOG" 2>&1 || fail 'owned upgrade fixture cleanup failed'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "upgrade:$resource_type" upgrade passed '' '' "$migration_code"
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
  assemble_workspace "$resource_type" "$fixtures" || fail 'import producer fixture assembly failed'
  producer=$WORKSPACE
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

  importer=$SCRATCH/importer
  mkdir -m 700 "$importer"
  cp "$producer"/*.tf "$importer"/
  # The producer remains authoritative owner. A private state snapshot gives
  # the importer dependency context without detaching anything from producer.
  cp "$producer/terraform.tfstate" "$importer/terraform.tfstate"
  (cd "$importer" && run_cli state rm "$address") >>"$LOG" 2>&1 || fail 'importer target preparation failed'
  (cd "$importer" && run_cli import "$address" "$(cat "$SCRATCH/import-id")") >>"$LOG" 2>&1 || fail 'import failed'
  (cd "$importer" && run_cli refresh) >>"$LOG" 2>&1 || fail 'post-import refresh failed'
  set +e
  (cd "$importer" && run_cli plan -detailed-exitcode) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'post-import plan contains drift'
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
  (cd "$importer" && run_cli import "$address" "$(cat "$SCRATCH/import-id")") >>"$LOG" 2>&1
  absence_status=$?
  set -e
  [ "$absence_status" -ne 0 ] || fail 'destroyed import target remained authoritative'
  CLEANUP_MODE=none
  rm -rf "$producer" "$importer"
  WORKSPACE=
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
  run_cli fmt -check "$WORKSPACE" >>"$LOG" 2>&1 || return 1
  current_rc=$SCRATCH/current.tfrc
  write_current_config "$current_rc"
  export TF_CLI_CONFIG_FILE=$current_rc
}

run_replacement() {
  name=$1 fixture=$2 address=$3 dependency=$4
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
  python3 - "$SCRATCH/replacement-plan.json" "$address" "$name" "$SCRIPT_DIR/matrix.json" <<'PY' || fail 'ordered target-only replacement contract failed'
import json,sys
value=json.load(open(sys.argv[1],encoding="utf-8")); address=sys.argv[2]; name=sys.argv[3]
matrix=json.load(open(sys.argv[4],encoding="utf-8"))
scenario=next(item for item in matrix["replacement_scenarios"] if item["name"]==name)
changes=[c for c in value.get("resource_changes",[]) if c.get("address")==address]
if len(changes)!=1 or changes[0].get("change",{}).get("actions",[]) != scenario["expected_actions"]: raise SystemExit(1)
for change in value.get("resource_changes",[]):
  if change.get("address") == address: continue
  if change.get("change",{}).get("actions",[]) not in ([],["no-op"]): raise SystemExit(1)
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
  (cd "$WORKSPACE" && run_cli plan -detailed-exitcode -var=replacement_phase=after) >>"$LOG" 2>&1
  plan_status=$?
  set -e
  [ "$plan_status" -eq 0 ] || fail 'post-replacement plan contains drift'
  (cd "$WORKSPACE" && run_cli destroy -auto-approve -var=replacement_phase=after) >>"$LOG" 2>&1 || fail 'replacement cleanup failed'
  [ -z "$(cd "$WORKSPACE" && run_cli state list 2>>"$LOG")" ] || fail 'replacement state was not empty after destroy'
  set +e
  (cd "$WORKSPACE" && run_cli import "$address" "$(cat "$SCRATCH/replacement-after")") >>"$LOG" 2>&1
  absence_status=$?
  set -e
  [ "$absence_status" -ne 0 ] || fail 'replacement target remained authoritative after destroy'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
  record "replacement:$name" replacement passed ''
}

run_failure_recovery() {
  name=$1 fixture=$2 expected_title=$3 endpoint=$4 dependency=$5 target=$6
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
  (cd "$WORKSPACE" && run_cli apply -json -auto-approve -var=recover_create=true -var="litellm_api_base=$proxy_base") >"$SCRATCH/failure.jsonl" 2>>"$LOG"
  failed_status=$?
  set -e
  [ "$failed_status" -ne 0 ] || fail 'controlled pre-commit fault unexpectedly succeeded'
  python3 - "$SCRATCH/failure.jsonl" "$expected_title" <<'PYDIAG' || fail 'failure diagnostic did not map to the exact allowlisted title/code'
import json,sys
allowed={"Client Error":"model-create-error","Team Member Create Error":"team-member-create-error"}
expected=sys.argv[2]
if expected not in allowed: raise SystemExit(1)
titles=[]
for line in open(sys.argv[1],encoding="utf-8",errors="replace"):
  try: value=json.loads(line)
  except json.JSONDecodeError: continue
  diagnostic=value.get("diagnostic",{})
  if isinstance(diagnostic,dict) and isinstance(diagnostic.get("summary"),str): titles.append(diagnostic["summary"])
if expected not in titles or any(title not in allowed for title in titles): raise SystemExit(1)
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
  (cd "$WORKSPACE" && run_cli import "$target" "$(cat "$SCRATCH/recovery-id")") >>"$LOG" 2>&1
  absence_status=$?
  set -e
  [ "$absence_status" -ne 0 ] || fail 'recovery target remained authoritative after cleanup'
  CLEANUP_MODE=none
  rm -rf "$WORKSPACE"
  WORKSPACE=
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
[ "$PHASE" = all ] || [ "$PHASE" = upgrade ] || fail 'MATRIX_PHASE must be all or upgrade'

if [ "$PHASE" = all ]; then
# Existing lifecycle matrix covers all OSS resources and data sources. Route
# its historical `terraform` command name to the selected Terraform/OpenTofu
# binary so the protocol matrix cannot silently fall back to PATH Terraform.
selected_cli=$(command -v "$CLI") || fail 'selected CLI is not executable'
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
 print("\t".join([r["type"], ",".join(r["fixture"]), r["address"], r["import_expression"], r["lane"]]))
PY
while IFS="	" read -r resource_type fixtures address expression lane; do
  run_upgrade "$resource_type" "$fixtures" "$lane"
done <"$SCRATCH/resources.tsv"
if [ "$PHASE" = upgrade ]; then
  printf '%s\n' 'Upgrade-only diagnostic phase completed; no report was written'
  exit 0
fi
while IFS="	" read -r resource_type fixtures address expression lane; do
  run_import "$resource_type" "$fixtures" "$address" "$expression" "$lane"
done <"$SCRATCH/resources.tsv"

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/replacements.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["replacement_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["address"],r["dependency_address"]]))
PY
while IFS="	" read -r scenario_name fixture address dependency; do
  run_replacement "$scenario_name" "$fixture" "$address" "$dependency"
done <"$SCRATCH/replacements.tsv"

python3 - "$SCRIPT_DIR/matrix.json" <<'PY' >"$SCRATCH/recovery.tsv"
import json,sys
for r in json.load(open(sys.argv[1], encoding="utf-8"))["failure_recovery_scenarios"]:
 print("\t".join([r["name"],r["fixture"],r["expected_diagnostic_title"],r["fault_endpoint"],r["dependency_address"] or "-",r["target_address"]]))
PY
while IFS="	" read -r scenario_name fixture expected_title endpoint dependency target; do
  run_failure_recovery "$scenario_name" "$fixture" "$expected_title" "$endpoint" "$dependency" "$target"
done <"$SCRATCH/recovery.tsv"

python3 "$SCRIPT_DIR/harness.py" report-records \
  --records "$RESULTS" --report "$REPORT" --cli "$CLI" \
  --provider-binary "$PROVIDER_BINARY"
printf '%s\n' 'Matrix passed; report derives only from validated execution records'
