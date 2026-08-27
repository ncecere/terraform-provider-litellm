#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd -P)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/issue210-private-trigger.XXXXXX")
cleanup() { rm -rf "$scratch"; }
trap cleanup EXIT INT TERM HUP
chmod 700 "$scratch"

python3 - "$scratch" <<'PY'
import copy
import json
import sys
from pathlib import Path

root = Path(sys.argv[1])
source = "registry.terraform.io/ncecere/litellm"
identity = {"type": "string", "computed": True}
schema = {"provider_schemas": {source: {"resource_schemas": {
    "litellm_agent": {"version": 0, "block": {"attributes": {
        "id": identity,
        "agent_name": {"type": "string", "computed": True},
    }, "block_types": {}}},
}}}}
matrix = {
    "upgrade_expected_private_migrations": ["litellm_agent"],
    "upgrade_expected_private_plan_triggers": {"litellm_agent": ["id"]},
}
before = {"id": "synthetic-prior", "agent_name": "synthetic-agent"}
after = {"id": None, "agent_name": "synthetic-agent"}

def plan(actions=None, old=None, new=None, unknown=None, before_sensitive=None,
         after_sensitive=None):
    return {"resource_changes": [{
        "address": "litellm_agent.test", "mode": "managed",
        "type": "litellm_agent", "name": "test", "change": {
            "actions": ["update"] if actions is None else actions,
            "before": copy.deepcopy(before if old is None else old),
            "after": copy.deepcopy(after if new is None else new),
            "after_unknown": copy.deepcopy({"id": True} if unknown is None else unknown),
            "before_sensitive": before_sensitive or {},
            "after_sensitive": after_sensitive or {},
        },
    }]}

def write(name, value):
    (root / name).write_text(json.dumps(value), encoding="utf-8")

write("schema.json", schema)
write("matrix.json", matrix)
write("success.json", plan())
write("success-omitted.json", plan(new={"agent_name": before["agent_name"]}))
rejections = {
    "create": plan(actions=["create"]),
    "delete": plan(actions=["delete"]),
    "replacement": plan(actions=["create", "delete"]),
    "no-op": plan(actions=["no-op"]),
    "empty-actions": plan(actions=[]),
    "unknown-prior": plan(old={**before, "id": None}),
    "empty-prior": plan(old={**before, "id": ""}),
    "known-after": plan(new={**after, "id": "known-change"}),
    "null-after": plan(unknown={}),
    "false-unknown": plan(unknown={"id": False}),
    "sensitive-before": plan(before_sensitive={"id": True}),
    "sensitive-after": plan(after_sensitive={"id": True}),
    "extra-change": plan(new={**after, "agent_name": "changed"}),
    "extra-unknown": plan(unknown={"id": True, "agent_name": True}),
    "nested-alias": plan(unknown={"alias": {"id": True}}),
}
for name, value in rejections.items():
    write("reject-" + name + ".json", value)

bad_contracts = {
    "nested-path": ({**matrix, "upgrade_expected_private_plan_triggers": {"litellm_agent": ["profile.id"]}}, schema),
    "duplicate-path": ({**matrix, "upgrade_expected_private_plan_triggers": {"litellm_agent": ["id", "id"]}}, schema),
    "not-private": ({**matrix, "upgrade_expected_private_migrations": []}, schema),
    "ordinary-id-mask": ({**matrix, "upgrade_expected_computed_migrations": {"litellm_agent": ["id"]}}, schema),
    "missing-resource": ({**matrix, "upgrade_expected_private_plan_triggers": {"litellm_missing": ["id"]}}, schema),
}
for name, (bad_matrix, selected_schema) in bad_contracts.items():
    write("matrix-" + name + ".json", bad_matrix)
    write("schema-" + name + ".json", selected_schema)
for name, replacement in (
    ("not-computed", {"type": "string"}),
    ("sensitive", {"type": "string", "computed": True, "sensitive": True}),
    ("non-string", {"type": "number", "computed": True}),
    ("missing-id", None),
):
    selected = copy.deepcopy(schema)
    attributes = selected["provider_schemas"][source]["resource_schemas"]["litellm_agent"]["block"]["attributes"]
    if replacement is None:
        attributes.pop("id")
    else:
        attributes["id"] = replacement
    write("schema-" + name + ".json", selected)
    write("matrix-" + name + ".json", matrix)

show_state = {"values": {"root_module": {"resources": [{
    "address": "litellm_agent.test", "mode": "managed", "type": "litellm_agent",
    "name": "test", "schema_version": 0, "values": before,
}]}}}
raw_absent = {"resources": [{"type": "litellm_agent", "name": "test", "instances": [{"private": ""}]}]}
raw_present = {"resources": [{"type": "litellm_agent", "name": "test", "instances": [{"private": "reviewed"}]}]}
write("state-before.json", show_state)
write("state-after.json", show_state)
for name, values in (
    ("identity-changed", {**before, "id": "changed"}),
    ("public-changed", {**before, "agent_name": "changed"}),
    ("identity-missing", {"agent_name": before["agent_name"]}),
):
    selected = copy.deepcopy(show_state)
    selected["values"]["root_module"]["resources"][0]["values"] = values
    write("state-" + name + ".json", selected)
write("raw-absent.json", raw_absent)
write("raw-present.json", raw_present)
write("raw-present-before.json", raw_present)
PY

review() {
  plan=$1 schema=$2 matrix=$3 baseline=${4:-}
  set -- review-plan --plan "$plan" --schema "$schema" --matrix "$matrix" \
    --resource-type litellm_agent
  [ -z "$baseline" ] || set -- "$@" --private-trigger-baseline "$baseline"
  python3 "$SCRIPT_DIR/upgrade_state.py" "$@"
}

index=0
for plan in "$scratch/success.json" "$scratch/success-omitted.json"; do
  baseline=$scratch/baseline-$index.json
  result=$(review "$plan" "$scratch/schema.json" "$scratch/matrix.json" "$baseline")
  [ "$result" = upgrade-reviewed-private-plan-trigger ]
  [ -s "$baseline" ]
  index=$((index + 1))
done
for plan in "$scratch"/reject-*.json; do
  if review "$plan" "$scratch/schema.json" "$scratch/matrix.json" >/dev/null 2>&1; then
    echo 'adversarial private trigger plan unexpectedly passed' >&2
    exit 1
  fi
done
for matrix in "$scratch"/matrix-*.json; do
  suffix=${matrix##*/matrix-}
  schema=$scratch/schema-$suffix
  if review "$scratch/success.json" "$schema" "$matrix" >/dev/null 2>&1; then
    echo 'adversarial private trigger contract unexpectedly passed' >&2
    exit 1
  fi
done

compare_private_trigger() {
  python3 "$SCRIPT_DIR/upgrade_state.py" compare \
    --before "$1" --after "$2" \
    --schema "$scratch/schema.json" --resource-type litellm_agent \
    --raw-before "$3" --raw-after "$4" \
    --matrix "$scratch/matrix.json" --require-reviewed-private-migration
}

result=$(compare_private_trigger \
  "$scratch/state-before.json" "$scratch/state-after.json" \
  "$scratch/raw-absent.json" "$scratch/raw-present.json")
[ "$result" = upgrade-reviewed-private-plan-trigger-migration ]
for after in "$scratch"/state-identity-changed.json \
             "$scratch"/state-public-changed.json \
             "$scratch"/state-identity-missing.json; do
  if compare_private_trigger \
    "$scratch/state-before.json" "$after" \
    "$scratch/raw-absent.json" "$scratch/raw-present.json" >/dev/null 2>&1; then
    echo 'non-exact post-apply public state unexpectedly passed' >&2
    exit 1
  fi
done
if compare_private_trigger \
  "$scratch/state-before.json" "$scratch/state-after.json" \
  "$scratch/raw-absent.json" "$scratch/raw-absent.json" >/dev/null 2>&1; then
  echo 'missing reviewed private migration unexpectedly passed' >&2
  exit 1
fi
if compare_private_trigger \
  "$scratch/state-before.json" "$scratch/state-after.json" \
  "$scratch/raw-present-before.json" "$scratch/raw-absent.json" >/dev/null 2>&1; then
  echo 'private provenance removal unexpectedly passed' >&2
  exit 1
fi
