#!/usr/bin/env python3
"""Schema-directed, secret-safe Terraform upgrade state comparison."""

import argparse
import hashlib
import hmac
import json
import math
import os
import re
import secrets
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Mapping, Optional, Sequence, Tuple


PROVIDER_SOURCE = "registry.terraform.io/ncecere/litellm"
_COLLECTION_MODES = {"list", "set", "map"}
_NESTING_MODES = {"single", *_COLLECTION_MODES}
_MISSING = object()


class UpgradeStateError(ValueError):
    """Raised when state, schema, or an upgrade comparison is unsafe."""


@dataclass(frozen=True)
class TypedAbsence:
    """A null/omitted value tagged with its current-schema shape."""

    shape: str


_MIGRATION_PATH_PART = re.compile(r"([A-Za-z_][A-Za-z0-9_]*)(\[\*\])?")
_MIGRATION_TERMINAL = object()
_MASKED_MIGRATION_LEAF = TypedAbsence("reviewed-computed-migration-leaf")


def _parse_migration_path(path: str) -> Tuple[Tuple[str, bool], ...]:
    if not path:
        raise UpgradeStateError("computed migration contains an empty path")
    parts = path.split(".")
    result = []
    for part in parts:
        match = _MIGRATION_PATH_PART.fullmatch(part)
        if match is None:
            raise UpgradeStateError("computed migration contains a malformed path")
        result.append((match.group(1), match.group(2) is not None))
    return tuple(result)


def _validate_path_collection(wildcard: bool, mode: str, context: str) -> None:
    collection = mode in _COLLECTION_MODES
    if wildcard != collection:
        if wildcard:
            raise UpgradeStateError(context + " uses a wildcard on a non-collection")
        raise UpgradeStateError(context + " must use [*] for collection traversal")


def _validate_migration_path(
    parts: Tuple[Tuple[str, bool], ...], block_schema: Any, context: str,
    root: bool = True,
) -> None:
    block = _validate_block_schema(block_schema, context)
    attributes = _schema_map(block.get("attributes", {}), context + " schema attributes")
    block_types = _schema_map(block.get("block_types", {}), context + " schema block_types")
    name, wildcard = parts[0]
    terminal = len(parts) == 1
    if name in block_types:
        nested = _mapping(block_types[name], context + "." + name + " schema")
        mode = nested.get("nesting_mode")
        _validate_path_collection(wildcard, mode, context + "." + name)
        if terminal:
            raise UpgradeStateError("computed migration path cannot select a whole structure")
        _validate_migration_path(
            parts[1:], nested.get("block"), context + "." + name + "[]", False
        )
        return
    if name not in attributes:
        raise UpgradeStateError("computed migration path is absent from the current schema")
    meta = _validate_attribute_schema(attributes[name], context + "." + name)
    if meta.get("sensitive", False):
        raise UpgradeStateError("computed migration path traverses sensitive schema")
    if terminal:
        if wildcard or "nested_type" in meta:
            raise UpgradeStateError("computed migration path cannot select a whole structure")
        computed = meta.get("computed", False)
        if not isinstance(computed, bool):
            raise UpgradeStateError("computed migration leaf has malformed computed metadata")
        if not computed:
            raise UpgradeStateError("computed migration leaf is not computed")
        if root and name == "id":
            raise UpgradeStateError("resource identity requires an identity migration")
        return
    if "nested_type" not in meta:
        raise UpgradeStateError("computed migration path traverses a non-nested attribute")
    nested = _mapping(meta["nested_type"], context + "." + name + " nested schema")
    mode = nested.get("nesting_mode")
    _validate_path_collection(wildcard, mode, context + "." + name)
    child = {"attributes": nested.get("attributes"), "block_types": {}}
    _validate_migration_path(parts[1:], child, context + "." + name + "[]", False)


def _compile_migration_paths(provider_schema: Any, value: Any) -> Dict[str, Dict[Any, Any]]:
    selected_schema = _mapping(provider_schema, "provider schema")
    resource_schemas = _schema_map(selected_schema.get("resource_schemas"), "provider resource_schemas")
    configured = _mapping(value, "computed migrations")
    output: Dict[str, Dict[Any, Any]] = {}
    for resource_type, raw_paths in configured.items():
        if resource_type not in resource_schemas:
            raise UpgradeStateError("computed migration resource is absent from the current schema")
        paths = _sequence(raw_paths, "computed migrations." + resource_type)
        parsed = []
        seen = set()
        for raw_path in paths:
            if not isinstance(raw_path, str):
                raise UpgradeStateError("computed migrations contain a malformed path")
            parts = _parse_migration_path(raw_path)
            if parts in seen:
                raise UpgradeStateError("computed migrations contain a duplicate path")
            seen.add(parts)
            parsed.append(parts)
        for index, parts in enumerate(parsed):
            for other in parsed[index + 1:]:
                common = min(len(parts), len(other))
                if parts[:common] == other[:common] and len(parts) != len(other):
                    raise UpgradeStateError("computed migrations contain overlapping paths")
        schema_entry = _mapping(resource_schemas[resource_type], resource_type + " schema")
        block = schema_entry.get("block")
        root: Dict[Any, Any] = {}
        for parts in parsed:
            _validate_migration_path(parts, block, resource_type)
            node = root
            for part in parts:
                node = node.setdefault(part, {})
            node[_MIGRATION_TERMINAL] = True
        output[resource_type] = root
    return output


def _compile_private_plan_triggers(
    provider_schema: Any, value: Any, reviewed_private_migrations: Any,
) -> set[str]:
    selected_schema = _mapping(provider_schema, "provider schema")
    resource_schemas = _schema_map(
        selected_schema.get("resource_schemas"), "provider resource_schemas"
    )
    private_items = _sequence(reviewed_private_migrations, "private migrations")
    if any(not isinstance(item, str) or not item for item in private_items):
        raise UpgradeStateError("private migrations contain a malformed resource type")
    if len(private_items) != len(set(private_items)):
        raise UpgradeStateError("private migrations contain a duplicate resource type")
    reviewed_private = set(private_items)
    configured = _mapping(value, "reviewed private plan triggers")
    output: set[str] = set()
    for resource_type, raw_paths in configured.items():
        if resource_type not in resource_schemas:
            raise UpgradeStateError(
                "reviewed private plan trigger resource is absent from the current schema"
            )
        if resource_type not in reviewed_private:
            raise UpgradeStateError(
                "reviewed private plan trigger resource lacks a private migration"
            )
        paths = _sequence(
            raw_paths, "reviewed private plan triggers." + resource_type
        )
        if paths != ["id"]:
            raise UpgradeStateError(
                "reviewed private plan trigger must be exact top-level id"
            )
        schema_entry = _mapping(
            resource_schemas[resource_type], resource_type + " schema"
        )
        block = _validate_block_schema(schema_entry.get("block"), resource_type)
        attributes = _schema_map(
            block.get("attributes", {}), resource_type + " schema attributes"
        )
        if "id" not in attributes:
            raise UpgradeStateError(
                "reviewed private plan trigger identity is absent from the current schema"
            )
        identity = _validate_attribute_schema(
            attributes["id"], resource_type + ".id"
        )
        if identity.get("type") != "string":
            raise UpgradeStateError(
                "reviewed private plan trigger identity is not a string"
            )
        if identity.get("sensitive", False):
            raise UpgradeStateError(
                "reviewed private plan trigger identity is sensitive"
            )
        if identity.get("computed") is not True:
            raise UpgradeStateError(
                "reviewed private plan trigger identity is not computed"
            )
        output.add(resource_type)
    return output


def compile_upgrade_contract(
    provider_schema: Any, matrix: Any,
) -> Tuple[Dict[str, Dict[Any, Any]], set[str]]:
    """Compile all reviewed upgrade exceptions against the current schema."""
    matrix_obj = _mapping(matrix, "matrix")
    migration_masks = _compile_migration_paths(
        provider_schema, matrix_obj.get("upgrade_expected_computed_migrations", {})
    )
    private_triggers = _compile_private_plan_triggers(
        provider_schema,
        matrix_obj.get("upgrade_expected_private_plan_triggers", {}),
        matrix_obj.get("upgrade_expected_private_migrations", []),
    )
    return migration_masks, private_triggers


def _migration_child(mask: Optional[Mapping[Any, Any]], name: str) -> Optional[Mapping[Any, Any]]:
    if not mask:
        return None
    matches = [mask[key] for key in ((name, False), (name, True)) if key in mask]
    if len(matches) > 1:
        raise UpgradeStateError("computed migration path mask is ambiguous")
    return matches[0] if matches else None


def _mapping(value: Any, context: str) -> Mapping[str, Any]:
    if not isinstance(value, dict) or any(not isinstance(key, str) for key in value):
        raise UpgradeStateError(context + " must be an object")
    return value


def _sequence(value: Any, context: str) -> Sequence[Any]:
    if not isinstance(value, list):
        raise UpgradeStateError(context + " must be an array")
    return value


def _schema_map(value: Any, context: str) -> Mapping[str, Any]:
    result = _mapping(value, context)
    if any(not name for name in result):
        raise UpgradeStateError(context + " contains an empty name")
    return result


def _absence(shape: Any) -> TypedAbsence:
    return TypedAbsence(json.dumps(shape, sort_keys=True, separators=(",", ":")))


def _validate_type_schema(type_schema: Any, context: str) -> None:
    if isinstance(type_schema, str):
        if type_schema not in {"string", "bool", "number", "dynamic"}:
            raise UpgradeStateError(context + " has an unsupported attribute type")
        return
    parts = _sequence(type_schema, context + " schema type")
    if len(parts) != 2 or not isinstance(parts[0], str):
        raise UpgradeStateError(context + " has a malformed attribute type")
    kind, element_schema = parts
    if kind in {"list", "set", "map"}:
        _validate_type_schema(element_schema, context + " element")
        return
    if kind == "object":
        fields = _schema_map(element_schema, context + " object schema")
        for name, child_schema in fields.items():
            _validate_type_schema(child_schema, context + "." + name)
        return
    if kind == "tuple":
        for index, child_schema in enumerate(_sequence(element_schema, context + " tuple schema")):
            _validate_type_schema(child_schema, context + " tuple[]")
        return
    raise UpgradeStateError(context + " has an unsupported collection type")


def _validate_attribute_schema(meta: Any, context: str) -> Mapping[str, Any]:
    selected = _mapping(meta, context + " schema")
    sensitive = selected.get("sensitive", False)
    if not isinstance(sensitive, bool):
        raise UpgradeStateError(context + " has malformed sensitivity metadata")
    has_type = "type" in selected
    has_nested = "nested_type" in selected
    if has_type == has_nested:
        raise UpgradeStateError(context + " schema must contain exactly one type shape")
    if has_type:
        _validate_type_schema(selected["type"], context)
    else:
        nested = _mapping(selected["nested_type"], context + " nested schema")
        mode = nested.get("nesting_mode")
        if mode not in _NESTING_MODES:
            raise UpgradeStateError(context + " has an unsupported nesting mode")
        attributes = _schema_map(nested.get("attributes"), context + " nested schema attributes")
        for name, child_meta in attributes.items():
            _validate_attribute_schema(child_meta, context + "." + name)
    return selected


def _validate_block_schema(block_schema: Any, context: str) -> Mapping[str, Any]:
    block = _mapping(block_schema, context + " schema")
    attributes = _schema_map(block.get("attributes", {}), context + " schema attributes")
    block_types = _schema_map(block.get("block_types", {}), context + " schema block_types")
    for name, meta in attributes.items():
        _validate_attribute_schema(meta, context + "." + name)
    for name, raw_nested in block_types.items():
        nested = _mapping(raw_nested, context + "." + name + " schema")
        if nested.get("nesting_mode") not in _NESTING_MODES:
            raise UpgradeStateError(context + "." + name + " has an unsupported nesting mode")
        if "block" not in nested:
            raise UpgradeStateError(context + "." + name + " schema has no block")
        _validate_block_schema(nested["block"], context + "." + name + " block")
    return block


def _sortable(value: Any) -> Any:
    if isinstance(value, TypedAbsence):
        return ["absence", value.shape]
    if isinstance(value, dict):
        return ["object", [[key, _sortable(child)] for key, child in sorted(value.items())]]
    if isinstance(value, list):
        return ["array", [_sortable(child) for child in value]]
    if value is None:
        return ["null"]
    if isinstance(value, bool):
        return ["bool", value]
    if isinstance(value, (int, float)):
        return ["number", value]
    if isinstance(value, str):
        return ["string", value]
    raise UpgradeStateError("canonical state contains an unsupported value")


def _sort_key(value: Any) -> str:
    return json.dumps(_sortable(value), sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def _canonical_dynamic(value: Any, context: str) -> Any:
    if value is None or isinstance(value, (str, bool)):
        return value
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if isinstance(value, float) and not math.isfinite(value):
            raise UpgradeStateError(context + " contains a non-finite number")
        return value
    if isinstance(value, list):
        return [_canonical_dynamic(child, context + "[]") for child in value]
    if isinstance(value, dict):
        obj = _mapping(value, context)
        return {key: _canonical_dynamic(obj[key], context + ".*") for key in sorted(obj)}
    raise UpgradeStateError(context + " contains an unsupported JSON value")


def _canonical_type(value: Any, type_schema: Any, context: str) -> Any:
    _validate_type_schema(type_schema, context)
    if value is _MISSING or value is None:
        return _absence(["attribute", type_schema])
    if isinstance(type_schema, str):
        if type_schema == "string":
            if not isinstance(value, str):
                raise UpgradeStateError(context + " must be a string or null")
            return value
        if type_schema == "bool":
            if not isinstance(value, bool):
                raise UpgradeStateError(context + " must be a boolean or null")
            return value
        if type_schema == "number":
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                raise UpgradeStateError(context + " must be a number or null")
            if isinstance(value, float) and not math.isfinite(value):
                raise UpgradeStateError(context + " contains a non-finite number")
            return value
        if type_schema == "dynamic":
            return _canonical_dynamic(value, context)
        raise UpgradeStateError(context + " has an unsupported attribute type")
    parts = _sequence(type_schema, context + " schema type")
    if len(parts) != 2 or not isinstance(parts[0], str):
        raise UpgradeStateError(context + " has a malformed attribute type")
    kind, element_schema = parts
    if kind in ("list", "set"):
        items = _sequence(value, context)
        result = [_canonical_type(child, element_schema, context + "[]") for child in items]
        if kind == "set":
            result.sort(key=_sort_key)
        return result
    if kind == "map":
        obj = _mapping(value, context)
        return {key: _canonical_type(obj[key], element_schema, context + ".*") for key in sorted(obj)}
    if kind == "object":
        obj = _mapping(value, context)
        fields = _schema_map(element_schema, context + " object schema")
        return {
            name: _canonical_type(obj.get(name, _MISSING), fields[name], context + "." + name)
            for name in sorted(fields)
        }
    if kind == "tuple":
        items = _sequence(value, context)
        item_schemas = _sequence(element_schema, context + " tuple schema")
        if len(items) != len(item_schemas):
            raise UpgradeStateError(context + " has the wrong tuple length")
        return [_canonical_type(child, item_schemas[index], context + "[]") for index, child in enumerate(items)]
    raise UpgradeStateError(context + " has an unsupported collection type")


def _canonical_nested(
    value: Any, nested_schema: Any, context: str,
    mask: Optional[Mapping[Any, Any]] = None,
) -> Any:
    nested = _mapping(nested_schema, context + " schema")
    mode = nested.get("nesting_mode")
    if mode not in _NESTING_MODES:
        raise UpgradeStateError(context + " has an unsupported nesting mode")
    attributes = _schema_map(nested.get("attributes"), context + " schema attributes")
    shape = ["nested-attribute", mode]
    if value is _MISSING or value is None:
        return _absence(shape)

    def one(raw: Any, item_context: str) -> Dict[str, Any]:
        obj = _mapping(raw, item_context)
        result: Dict[str, Any] = {}
        for name in sorted(attributes):
            meta = _mapping(attributes[name], item_context + "." + name + " schema")
            canonical = _canonical_attribute(
                obj.get(name, _MISSING), meta, item_context + "." + name,
                _migration_child(mask, name),
            )
            sensitive = meta.get("sensitive", False)
            if not isinstance(sensitive, bool):
                raise UpgradeStateError(item_context + "." + name + " has malformed sensitivity metadata")
            if not sensitive:
                result[name] = canonical
        return result

    if mode == "single":
        return one(value, context)
    if mode in ("list", "set"):
        items = _sequence(value, context)
        result = [one(child, context + "[]") for child in items]
        if mode == "set":
            result.sort(key=_sort_key)
        return result
    obj = _mapping(value, context)
    return {key: one(obj[key], context + ".*") for key in sorted(obj)}


def _canonical_attribute(
    value: Any, meta: Mapping[str, Any], context: str,
    mask: Optional[Mapping[Any, Any]] = None,
) -> Any:
    selected = _validate_attribute_schema(meta, context)
    if "nested_type" in selected:
        return _canonical_nested(value, selected["nested_type"], context, mask)
    canonical = _canonical_type(value, selected["type"], context)
    if mask and _MIGRATION_TERMINAL in mask:
        return _MASKED_MIGRATION_LEAF
    return canonical


def _canonical_block(
    value: Any, block_schema: Any, context: str,
    mask: Optional[Mapping[Any, Any]] = None,
) -> Dict[str, Any]:
    block = _validate_block_schema(block_schema, context)
    obj = _mapping(value, context)
    attributes = _schema_map(block.get("attributes", {}), context + " schema attributes")
    block_types = _schema_map(block.get("block_types", {}), context + " schema block_types")
    result: Dict[str, Any] = {}
    for name in sorted(attributes):
        meta = _mapping(attributes[name], context + "." + name + " schema")
        canonical = _canonical_attribute(
            obj.get(name, _MISSING), meta, context + "." + name,
            _migration_child(mask, name),
        )
        sensitive = meta.get("sensitive", False)
        if not isinstance(sensitive, bool):
            raise UpgradeStateError(context + "." + name + " has malformed sensitivity metadata")
        if not sensitive:
            result[name] = canonical
    for name in sorted(block_types):
        meta = _mapping(block_types[name], context + "." + name + " schema")
        if "block" not in meta:
            raise UpgradeStateError(context + "." + name + " schema has no block")
        child_block = _mapping(meta["block"], context + "." + name + " block schema")
        # Blocks can recursively contain both attributes and child blocks. Use
        # a dedicated object callback rather than flattening those structures.
        mode = meta.get("nesting_mode")
        if mode not in _NESTING_MODES:
            raise UpgradeStateError(context + "." + name + " has an unsupported nesting mode")
        raw = obj.get(name, _MISSING)
        shape = ["block", mode]
        if raw is _MISSING or raw is None:
            result[name] = _absence(shape)
            continue

        child_mask = _migration_child(mask, name)

        def one_block(item: Any, item_context: str) -> Dict[str, Any]:
            return _canonical_block(item, child_block, item_context, child_mask)

        if mode == "single":
            result[name] = one_block(raw, context + "." + name)
        elif mode in ("list", "set"):
            items = _sequence(raw, context + "." + name)
            if not items:
                result[name] = _absence(shape)
            else:
                children = [one_block(child, context + "." + name + "[]") for child in items]
                if mode == "set":
                    children.sort(key=_sort_key)
                result[name] = children
        else:
            children = _mapping(raw, context + "." + name)
            result[name] = (
                _absence(shape) if not children else
                {key: one_block(children[key], context + "." + name + ".*") for key in sorted(children)}
            )
    return result


def canonicalize_resources(
    state: Any, provider_schema: Any,
    migration_masks: Optional[Mapping[str, Mapping[Any, Any]]] = None,
) -> Dict[str, Dict[str, Any]]:
    """Return current-schema, non-sensitive canonical managed-resource rows."""
    document = _mapping(state, "state")
    selected_schema = _mapping(provider_schema, "provider schema")
    resource_schemas = _schema_map(selected_schema.get("resource_schemas"), "provider resource_schemas")
    values = _mapping(document.get("values"), "state values")
    root = _mapping(values.get("root_module"), "state root_module")
    output: Dict[str, Dict[str, Any]] = {}

    def walk(module: Mapping[str, Any], context: str) -> None:
        resources = _sequence(module.get("resources", []), context + " resources")
        children = _sequence(module.get("child_modules", []), context + " child_modules")
        for index, raw_resource in enumerate(resources):
            resource = _mapping(raw_resource, context + " resource")
            mode = resource.get("mode")
            if mode not in {"managed", "data"}:
                raise UpgradeStateError(context + " resource mode is malformed")
            if mode != "managed":
                continue
            address, resource_type = resource.get("address"), resource.get("type")
            if not isinstance(address, str) or not address or not isinstance(resource_type, str) or not resource_type:
                raise UpgradeStateError(context + " managed resource identity is malformed")
            if address in output:
                raise UpgradeStateError("state contains a duplicate managed address")
            if resource_type not in resource_schemas:
                raise UpgradeStateError("state contains a managed resource absent from the current schema")
            schema_version = resource.get("schema_version", 0)
            if isinstance(schema_version, bool) or not isinstance(schema_version, int) or schema_version < 0:
                raise UpgradeStateError("state contains a malformed schema version")
            schema_entry = _mapping(resource_schemas[resource_type], resource_type + " schema")
            if "block" not in schema_entry:
                raise UpgradeStateError(resource_type + " schema has no root block")
            canonical = _canonical_block(
                resource.get("values"), schema_entry["block"], "state " + address,
                (migration_masks or {}).get(resource_type),
            )
            output[address] = {
                "type": resource_type,
                "schema_version": schema_version,
                "values": canonical,
            }
        for index, raw_child in enumerate(children):
            child = _mapping(raw_child, context + " child_module")
            walk(child, context + " child_module[]")

    walk(root, "state root_module")
    return output


def compare_state_values(
    before: Any, after: Any, provider_schema: Any, matrix: Any,
    *, exact_public: bool = False,
) -> bool:
    """Compare public state, returning whether a reviewed migration occurred."""
    matrix_obj = _mapping(matrix, "matrix")
    migration_masks, _ = compile_upgrade_contract(provider_schema, matrix_obj)
    if exact_public:
        migration_masks = {}
    schema_migrations = _mapping(matrix_obj.get("upgrade_expected_schema_migrations", {}), "schema migrations")
    identity_migrations = _mapping(matrix_obj.get("upgrade_expected_identity_migrations", {}), "identity migrations")
    left, right = canonicalize_resources(before, provider_schema), canonicalize_resources(after, provider_schema)
    masked_left = canonicalize_resources(before, provider_schema, migration_masks)
    masked_right = canonicalize_resources(after, provider_schema, migration_masks)
    if set(left) != set(right):
        raise UpgradeStateError("address set changed")
    migrated = False
    key = secrets.token_bytes(32)
    for address in sorted(left):
        if left[address]["type"] != right[address]["type"]:
            raise UpgradeStateError("resource type changed")
        resource_type = left[address]["type"]
        old_version, new_version = left[address]["schema_version"], right[address]["schema_version"]
        if old_version != new_version:
            reviewed = schema_migrations.get(resource_type)
            if (
                exact_public or not isinstance(reviewed, list)
                or reviewed != [old_version, new_version]
            ):
                raise UpgradeStateError("schema version changed without reviewed migration")
            migrated = True
        old_values = dict(left[address]["values"])
        new_values = dict(right[address]["values"])
        old_masked_values = dict(masked_left[address]["values"])
        new_masked_values = dict(masked_right[address]["values"])
        old_id = old_values.pop("id", _absence(["attribute", "string"]))
        new_id = new_values.pop("id", _absence(["attribute", "string"]))
        old_masked_values.pop("id", None)
        new_masked_values.pop("id", None)
        identity_rule = identity_migrations.get(resource_type)
        if exact_public:
            if (
                not isinstance(old_id, str) or not old_id.strip()
                or not isinstance(new_id, str) or not new_id.strip()
            ):
                raise UpgradeStateError("exact public comparison identity is not known")
            identity_rule = None
        if identity_rule == "sha256-of-prior-id":
            expected = "sha256:" + hashlib.sha256(str(old_id).encode()).hexdigest()
            if not hmac.compare_digest(expected, str(new_id)):
                raise UpgradeStateError("reviewed identity migration mismatch")
            migrated = True
        elif identity_rule is not None:
            raise UpgradeStateError("unsupported reviewed identity migration")
        elif not hmac.compare_digest(
            hmac.new(key, str(old_id).encode(), hashlib.sha256).digest(),
            hmac.new(key, str(new_id).encode(), hashlib.sha256).digest(),
        ):
            raise UpgradeStateError("resource identity changed")
        if old_masked_values != new_masked_values:
            changed = sorted(
                field for field in set(old_masked_values) | set(new_masked_values)
                if old_masked_values.get(field, _MISSING) != new_masked_values.get(field, _MISSING)
            )
            raise UpgradeStateError("nonsecret semantic state changed: " + resource_type + ":" + ",".join(changed))
        if old_values != new_values:
            migrated = True
    return migrated


def _true_plan_paths(value: Any, context: str) -> set[Tuple[Any, ...]]:
    result: set[Tuple[Any, ...]] = set()

    def walk(item: Any, path: Tuple[Any, ...]) -> None:
        if isinstance(item, bool):
            if item:
                result.add(path)
            return
        if isinstance(item, dict):
            obj = _mapping(item, context)
            for name, child in obj.items():
                walk(child, path + (name,))
            return
        if isinstance(item, list):
            for index, child in enumerate(item):
                walk(child, path + (index,))
            return
        raise UpgradeStateError(context + " contains a malformed unknown/sensitive marker")

    walk(value, ())
    return result


def _plan_state(resource: Mapping[str, Any], values: Mapping[str, Any]) -> Dict[str, Any]:
    address, resource_type = resource.get("address"), resource.get("type")
    if not isinstance(address, str) or not address:
        raise UpgradeStateError("upgrade plan resource address is malformed")
    if not isinstance(resource_type, str) or not resource_type:
        raise UpgradeStateError("upgrade plan resource type is malformed")
    return {"values": {"root_module": {"resources": [{
        "address": address,
        "mode": "managed",
        "type": resource_type,
        "name": resource.get("name", address),
        "schema_version": 0,
        "values": dict(values),
    }]}}}


def review_upgrade_plan(plan: Any, provider_schema: Any, matrix: Any) -> set[str]:
    """Review migration-only plan changes and identify private-only triggers."""
    document = _mapping(plan, "upgrade plan")
    matrix_obj = _mapping(matrix, "matrix")
    _, private_trigger_types = compile_upgrade_contract(provider_schema, matrix_obj)
    changes = _sequence(document.get("resource_changes", []), "upgrade plan resource_changes")
    triggered: set[str] = set()
    for raw_resource in changes:
        resource = _mapping(raw_resource, "upgrade plan resource")
        if resource.get("mode", "managed") != "managed":
            raise UpgradeStateError("upgrade plan contains a non-managed resource change")
        resource_type = resource.get("type")
        if not isinstance(resource_type, str) or not resource_type:
            raise UpgradeStateError("upgrade plan resource type is malformed")
        change = _mapping(resource.get("change"), "upgrade plan change")
        actions = _sequence(change.get("actions"), "upgrade plan actions")
        if actions not in ([], ["no-op"], ["update"]):
            raise UpgradeStateError("upgrade plan contains an unreviewed action")
        before = _mapping(change.get("before"), "upgrade plan prior values")
        after = _mapping(change.get("after"), "upgrade plan proposed values")
        after_unknown = _mapping(
            change.get("after_unknown", {}), "upgrade plan unknown markers"
        )
        before_sensitive = _mapping(
            change.get("before_sensitive", {}), "upgrade plan prior sensitive markers"
        )
        after_sensitive = _mapping(
            change.get("after_sensitive", {}), "upgrade plan sensitive markers"
        )
        unknown_paths = _true_plan_paths(after_unknown, "upgrade plan unknown markers")
        sensitive_paths = (
            _true_plan_paths(before_sensitive, "upgrade plan prior sensitive markers")
            | _true_plan_paths(after_sensitive, "upgrade plan sensitive markers")
        )
        changed_roots = {
            name for name in set(before) | set(after)
            if before.get(name, _MISSING) != after.get(name, _MISSING)
        }
        identity_unknown = ("id",) in unknown_paths
        if identity_unknown:
            if resource_type not in private_trigger_types:
                raise UpgradeStateError("upgrade plan contains an unreviewed identity trigger")
            if actions != ["update"]:
                raise UpgradeStateError("reviewed private plan trigger requires an update")
            old_id = before.get("id", _MISSING)
            if not isinstance(old_id, str) or not old_id.strip():
                raise UpgradeStateError("reviewed private plan trigger prior identity is not known")
            # Terraform's plan JSON may omit an unknown null attribute from
            # `after`; the exact top-level after_unknown marker is authoritative.
            if after.get("id") is not None:
                raise UpgradeStateError("reviewed private plan trigger proposed identity is known")
            if after_unknown.get("id") is not True or unknown_paths != {("id",)}:
                raise UpgradeStateError("reviewed private plan trigger has arbitrary unknown values")
            if any(path and path[0] == "id" for path in sensitive_paths):
                raise UpgradeStateError("reviewed private plan trigger identity is sensitive")
            if changed_roots != {"id"}:
                raise UpgradeStateError("reviewed private plan trigger changed another public value")
            normalized_after = dict(after)
            normalized_after["id"] = old_id
            if compare_state_values(
                _plan_state(resource, before), _plan_state(resource, normalized_after),
                provider_schema, matrix_obj, exact_public=True,
            ):
                raise UpgradeStateError("reviewed private plan trigger hid a public migration")
            triggered.add(resource_type)
            continue
        if any(path and path[0] == "id" for path in unknown_paths):
            raise UpgradeStateError("upgrade plan contains an unresolved identity alias")
        if sensitive_paths and any(path and path[0] in changed_roots for path in sensitive_paths):
            raise UpgradeStateError("upgrade plan changes a sensitive value")
        if actions in ([], ["no-op"]):
            if changed_roots or unknown_paths:
                raise UpgradeStateError("no-op upgrade plan contains changed values")
            continue
        if not changed_roots:
            raise UpgradeStateError("upgrade update contains no changed public values")
        migrated = compare_state_values(
            _plan_state(resource, before), _plan_state(resource, after),
            provider_schema, matrix_obj,
        )
        if not migrated:
            raise UpgradeStateError("upgrade plan changed without a reviewed migration")
    return triggered


def private_trigger_plan_baseline(
    plan: Any, provider_schema: Any, matrix: Any, resource_type: str,
) -> Dict[str, Any]:
    """Return the reviewed trigger's refreshed prior public state document."""
    triggered = review_upgrade_plan(plan, provider_schema, matrix)
    if triggered != {resource_type}:
        raise UpgradeStateError(
            "reviewed private plan trigger does not match the baseline subject"
        )
    document = _mapping(plan, "upgrade plan")
    candidates = []
    for raw_resource in _sequence(
        document.get("resource_changes", []), "upgrade plan resource_changes"
    ):
        resource = _mapping(raw_resource, "upgrade plan resource")
        if resource.get("type") != resource_type:
            continue
        change = _mapping(resource.get("change"), "upgrade plan change")
        unknown = _mapping(
            change.get("after_unknown", {}), "upgrade plan unknown markers"
        )
        if _true_plan_paths(unknown, "upgrade plan unknown markers") == {("id",)}:
            candidates.append((resource, _mapping(
                change.get("before"), "upgrade plan prior values"
            )))
    if len(candidates) != 1:
        raise UpgradeStateError(
            "reviewed private plan trigger baseline is not an exact resource"
        )
    resource, values = candidates[0]
    schemas = _schema_map(
        _mapping(provider_schema, "provider schema").get("resource_schemas"),
        "provider resource_schemas",
    )
    schema_version = _mapping(
        schemas[resource_type], resource_type + " schema"
    ).get("version", 0)
    if (
        isinstance(schema_version, bool) or not isinstance(schema_version, int)
        or schema_version < 0
    ):
        raise UpgradeStateError("reviewed private plan trigger schema version is malformed")
    address = resource.get("address")
    if not isinstance(address, str) or not address:
        raise UpgradeStateError("reviewed private plan trigger address is malformed")
    return {"values": {"root_module": {"resources": [{
        "address": address,
        "mode": "managed",
        "type": resource_type,
        "name": resource.get("name", address),
        "schema_version": schema_version,
        "values": dict(values),
    }]}}}


def _exclusive_json_write(path: Path, value: Any) -> None:
    encoded = (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags, 0o600)
    except OSError as error:
        raise UpgradeStateError("unable to create private plan baseline") from error
    try:
        written = 0
        while written < len(encoded):
            count = os.write(descriptor, encoded[written:])
            if count <= 0:
                raise UpgradeStateError("unable to write private plan baseline")
            written += count
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def private_signals(state: Any) -> Dict[Tuple[str, str, str, int], bool]:
    document = _mapping(state, "raw state")
    resources = _sequence(document.get("resources"), "raw state resources")
    result: Dict[Tuple[str, str, str, int], bool] = {}
    for raw_resource in resources:
        resource = _mapping(raw_resource, "raw state resource")
        module = resource.get("module", "")
        resource_type, name = resource.get("type"), resource.get("name")
        if not all(isinstance(item, str) for item in (module, resource_type, name)) or not resource_type or not name:
            raise UpgradeStateError("raw state resource identity is malformed")
        instances = _sequence(resource.get("instances"), "raw state instances")
        for index, raw_instance in enumerate(instances):
            instance = _mapping(raw_instance, "raw state instance")
            private = instance.get("private", "")
            if private is None:
                private = ""
            if not isinstance(private, str):
                raise UpgradeStateError("raw state private value is malformed")
            identity = (module, resource_type, name, index)
            if identity in result:
                raise UpgradeStateError("raw state contains a duplicate instance identity")
            result[identity] = bool(private)
    return result


def compare_private_state(
    before: Any, after: Any, reviewed_types: Any,
    required_type: Optional[str] = None,
) -> bool:
    items = _sequence(reviewed_types, "private migrations")
    if any(not isinstance(item, str) or not item for item in items):
        raise UpgradeStateError("private migrations contain a malformed resource type")
    reviewed = set(items)
    left, right = private_signals(before), private_signals(after)
    if set(left) != set(right):
        raise UpgradeStateError("provider-private address set changed")
    migrated = False
    required_changes = 0
    for identity in sorted(left):
        if left[identity] == right[identity]:
            continue
        if not left[identity] and right[identity] and identity[1] in reviewed:
            if required_type is not None and identity[1] != required_type:
                raise UpgradeStateError(
                    "provider-private change differs from the required reviewed migration"
                )
            migrated = True
            required_changes += int(identity[1] == required_type)
            continue
        raise UpgradeStateError("provider-private presence changed without reviewed migration")
    if required_type is not None:
        if required_type not in reviewed:
            raise UpgradeStateError("required provider-private migration is not reviewed")
        if required_changes != 1:
            raise UpgradeStateError(
                "reviewed private plan trigger did not produce its exact private migration"
            )
    return migrated


def _reject_constant(value: str) -> None:
    raise UpgradeStateError("JSON contains a non-finite number")


def load_json(path: Path) -> Any:
    def object_pairs(pairs: Sequence[Tuple[str, Any]]) -> Dict[str, Any]:
        result: Dict[str, Any] = {}
        for key, value in pairs:
            if key in result:
                raise UpgradeStateError("JSON contains a duplicate object key")
            result[key] = value
        return result

    try:
        with path.open(encoding="utf-8") as handle:
            return json.load(handle, object_pairs_hook=object_pairs, parse_constant=_reject_constant)
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise UpgradeStateError("unable to read strict JSON input") from error


def compare_files(args: argparse.Namespace) -> int:
    schema_document = _mapping(load_json(Path(args.schema)), "schema document")
    provider_schemas = _mapping(schema_document.get("provider_schemas"), "provider_schemas")
    if PROVIDER_SOURCE not in provider_schemas:
        raise UpgradeStateError("current provider schema is absent")
    provider_schema = provider_schemas[PROVIDER_SOURCE]
    matrix = _mapping(load_json(Path(args.matrix)), "matrix")
    before, after = load_json(Path(args.before)), load_json(Path(args.after))
    rows = canonicalize_resources(before, provider_schema)
    if args.resource_type not in _schema_map(_mapping(provider_schema, "provider schema").get("resource_schemas"), "resource_schemas"):
        raise UpgradeStateError("requested resource type is absent from current schema")
    if not any(row["type"] == args.resource_type for row in rows.values()):
        raise UpgradeStateError("requested resource type is absent from baseline state")
    required_private = args.resource_type if args.require_reviewed_private_migration else None
    _, private_trigger_types = compile_upgrade_contract(provider_schema, matrix)
    if required_private is not None and required_private not in private_trigger_types:
        raise UpgradeStateError("required provider-private migration lacks a reviewed plan trigger")
    migrated = compare_state_values(
        before, after, provider_schema, matrix,
        exact_public=required_private is not None,
    )
    private_migrated = compare_private_state(
        load_json(Path(args.raw_before)), load_json(Path(args.raw_after)),
        matrix.get("upgrade_expected_private_migrations", []), required_private,
    )
    migrated = private_migrated or migrated
    if required_private is not None:
        if not private_migrated or not migrated:
            raise UpgradeStateError(
                "reviewed private plan trigger did not produce a reviewed migration"
            )
        print("upgrade-reviewed-private-plan-trigger-migration")
    elif migrated:
        print("upgrade-reviewed-migration")
    return 0


def review_plan_files(args: argparse.Namespace) -> int:
    schema_document = _mapping(load_json(Path(args.schema)), "schema document")
    provider_schemas = _mapping(schema_document.get("provider_schemas"), "provider_schemas")
    if PROVIDER_SOURCE not in provider_schemas:
        raise UpgradeStateError("current provider schema is absent")
    matrix = _mapping(load_json(Path(args.matrix)), "matrix")
    triggered = review_upgrade_plan(
        load_json(Path(args.plan)), provider_schemas[PROVIDER_SOURCE], matrix
    )
    if triggered:
        if triggered != {args.resource_type}:
            raise UpgradeStateError(
                "reviewed private plan trigger does not match the upgrade subject"
            )
        if args.private_trigger_baseline:
            _exclusive_json_write(
                Path(args.private_trigger_baseline),
                private_trigger_plan_baseline(
                    load_json(Path(args.plan)), provider_schemas[PROVIDER_SOURCE],
                    matrix, args.resource_type,
                ),
            )
        print("upgrade-reviewed-private-plan-trigger")
    else:
        print("upgrade-plan-reviewed")
    return 0


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    compare = subparsers.add_parser("compare")
    compare.add_argument("--before", required=True)
    compare.add_argument("--after", required=True)
    compare.add_argument("--schema", required=True)
    compare.add_argument("--resource-type", required=True)
    compare.add_argument("--raw-before", required=True)
    compare.add_argument("--raw-after", required=True)
    compare.add_argument("--matrix", required=True)
    compare.add_argument("--require-reviewed-private-migration", action="store_true")
    compare.set_defaults(handler=compare_files)
    review = subparsers.add_parser("review-plan")
    review.add_argument("--plan", required=True)
    review.add_argument("--schema", required=True)
    review.add_argument("--matrix", required=True)
    review.add_argument("--resource-type", required=True)
    review.add_argument("--private-trigger-baseline")
    review.set_defaults(handler=review_plan_files)
    args = parser.parse_args(argv)
    try:
        return args.handler(args)
    except UpgradeStateError as error:
        print("upgrade state comparison failed: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
