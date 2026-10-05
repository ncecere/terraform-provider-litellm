#!/usr/bin/env python3
"""Export the reviewed LiteLLM API contract from live, directly registered routes."""

from __future__ import annotations

import argparse
import ast
import hashlib
import importlib
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any

UPSTREAM_REPOSITORY = "https://github.com/BerriAI/litellm"
UPSTREAM_TAG = "v1.104.0"
UPSTREAM_COMMIT = "79645770fedc7ec2627e6468d31062f20f82aecc"
PYTHON_VERSION = "3.12.14"
UV_LOCK_SHA256 = "4d02833751421f29facee660c303f6b02cf0282c2709e93c7ad5898bbc1c38de"
# v1.98.0 registered this organization update route with include_in_schema=False,
# so it had to be carried as a supplemental route. From v1.104.0 it is a normal
# public route; the exporter now requires it to be present in generated OpenAPI
# with its exact path-parameter contract instead of parsing it as hidden.
ORGANIZATION_UPDATE_ROUTE_SOURCE = Path("litellm/proxy/management_endpoints/organization_endpoints.py")
ORGANIZATION_UPDATE_ROUTE = ("PATCH", "/v2/organization/{organization_id}")
HTTP_METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}


def feature(name: str, module: str, prefixes: tuple[str, ...], *, suffixes: tuple[str, ...] = (), registration: str = "include_router", attribute: str = "router", mount_prefix: str = "", persistent_stub: bool = False) -> dict[str, Any]:
    return {
        "name": name,
        "module": module,
        "path_prefixes": list(prefixes),
        "path_suffixes": list(suffixes),
        "registration": registration,
        "attribute": attribute,
        "mount_prefix": mount_prefix,
        "persistent_swagger_stub": persistent_stub,
    }


# Exact source-reviewed LAZY_FEATURES order and mounting contract at UPSTREAM_COMMIT.
# The pinned source contains 35 definitions. v1.104.0 added llm_passthrough and
# claude_code_gateway and extended mcp_discoverable's prefixes.
EXPECTED_LAZY_FEATURES = (
    feature("guardrails", "litellm.proxy.guardrails.guardrail_endpoints", ("/guardrails", "/v2/guardrails", "/apply_guardrail", "/policies/usage")),
    feature("policies", "litellm.proxy.management_endpoints.policy_endpoints", ("/policy/", "/utils/test_policies_and_guardrails")),
    feature("policy_engine", "litellm.proxy.policy_engine.policy_endpoints", ("/policies",)),
    feature("policy_resolve", "litellm.proxy.policy_engine.policy_resolve_endpoints", ("/policies/resolve", "/policies/attachments/estimate-impact")),
    feature("agents", "litellm.proxy.agent_endpoints.endpoints", ("/v1/agents", "/agents", "/agent/")),
    feature("gemini_agents", "litellm.proxy.google_endpoints.agents_endpoints", ("/v1beta/agents",)),
    feature("a2a", "litellm.proxy.agent_endpoints.a2a_endpoints", ("/a2a",), suffixes=("/message/send",)),
    feature("a2a_registration", "litellm.proxy.a2a.endpoints", ("/v1/a2a/discover",)),
    feature("vector_stores", "litellm.proxy.vector_store_endpoints.endpoints", ("/v1/vector_stores", "/vector_stores", "/v1/indexes")),
    feature("vector_store_management", "litellm.proxy.vector_store_endpoints.management_endpoints", ("/vector_store/", "/v1/vector_store/")),
    feature("vector_store_files", "litellm.proxy.vector_store_files_endpoints.endpoints", ("/v1/vector_stores", "/vector_stores")),
    feature("tools", "litellm.proxy.management_endpoints.tool_management_endpoints", ("/v1/tool", "/tool")),
    feature("search_tools", "litellm.proxy.search_endpoints.search_tool_management", ("/search_tools",)),
    feature("mcp_management", "litellm.proxy.management_endpoints.mcp_management_endpoints", ("/v1/mcp/",)),
    feature("mcp_byok_oauth", "litellm.proxy._experimental.mcp_server.byok_oauth_endpoints", ("/v1/mcp/oauth", "/.well-known/oauth-")),
    feature("mcp_discoverable", "litellm.proxy._experimental.mcp_server.discoverable_endpoints", ("/.well-known/oauth-", "/.well-known/openid-configuration", "/.well-known/jwks.json", "/.well-known/litellm-cli-auth", "/authorize", "/token", "/callback", "/register", "/revoke", "/introspect"), suffixes=("/authorize", "/token", "/register")),
    feature("mcp_rest", "litellm.proxy._experimental.mcp_server.rest_endpoints", ("/mcp-rest",)),
    feature("mcp_app", "litellm.proxy._experimental.mcp_server.server", ("/mcp",), registration="mount_app", attribute="app", mount_prefix="/mcp", persistent_stub=True),
    feature("config_overrides", "litellm.proxy.management_endpoints.config_override_endpoints", ("/config_overrides",)),
    feature("llm_passthrough", "litellm.proxy.pass_through_endpoints.llm_passthrough_endpoints", ("/anthropic/", "/assemblyai/", "/azure/", "/azure_ai/", "/azure_speech/", "/bedrock/", "/cohere/", "/comprehendmedical", "/cursor/", "/deepgram/", "/eu.assemblyai/", "/fal_ai/", "/gemini/", "/gigachat/", "/milvus/", "/mistral/", "/nvidia_nim/", "/openai/", "/openai_passthrough/", "/tinyfish/", "/transcribe", "/typesafe/", "/openrouter/", "/vertex-ai/", "/vertex_ai/", "/vllm/", "/watsonx/")),
    feature("realtime", "litellm.proxy.realtime_endpoints.endpoints", ("/openai/v1/realtime", "/v1/realtime", "/realtime")),
    feature("anthropic_passthrough", "litellm.proxy.anthropic_endpoints.endpoints", ("/v1/messages", "/anthropic", "/api/event_logging")),
    feature("anthropic_skills", "litellm.proxy.anthropic_endpoints.skills_endpoints", ("/v1/skills", "/skills")),
    feature("claude_code_gateway", "litellm.proxy.anthropic_endpoints.gateway_endpoints", ("/claude_code_gateway",)),
    feature("langfuse_passthrough", "litellm.proxy.vertex_ai_endpoints.langfuse_endpoints", ("/langfuse",)),
    feature("evals", "litellm.proxy.openai_evals_endpoints.endpoints", ("/v1/evals", "/evals")),
    feature("claude_code_marketplace", "litellm.proxy.anthropic_endpoints.claude_code_endpoints", ("/claude-code",), attribute="claude_code_marketplace_router"),
    feature("scim", "litellm.proxy.management_endpoints.scim.scim_v2", ("/scim",), attribute="scim_router"),
    feature("cloudzero", "litellm.proxy.spend_tracking.cloudzero_endpoints", ("/cloudzero",)),
    feature("vantage", "litellm.proxy.spend_tracking.vantage_endpoints", ("/vantage",)),
    feature("usage_ai", "litellm.proxy.management_endpoints.usage_endpoints", ("/usage/ai",)),
    feature("prompts", "litellm.proxy.prompts.prompt_endpoints", ("/prompts", "/utils/dotprompt_json_converter")),
    feature("jwt_mappings", "litellm.proxy.management_endpoints.jwt_key_mapping_endpoints", ("/jwt/key/mapping",)),
    feature("compliance", "litellm.proxy.management_endpoints.compliance_endpoints", ("/compliance",)),
    feature("access_groups", "litellm.proxy.management_endpoints.access_group_endpoints", ("/access_group", "/v1/access_group", "/v1/unified_access_group")),
)


def run_git(root: Path, *args: str) -> str:
    return subprocess.run(["git", "-C", str(root), *args], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True).stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_checkout(root: Path) -> None:
    if run_git(root, "rev-parse", "HEAD") != UPSTREAM_COMMIT:
        raise RuntimeError(f"source checkout is not exact commit {UPSTREAM_COMMIT}")
    if UPSTREAM_TAG not in set(run_git(root, "tag", "--points-at", "HEAD").splitlines()):
        raise RuntimeError(f"commit is not tagged {UPSTREAM_TAG}")
    if run_git(root, "status", "--porcelain", "--untracked-files=no"):
        raise RuntimeError("source checkout has tracked modifications")
    if sha256(root / "uv.lock") != UV_LOCK_SHA256:
        raise RuntimeError("uv.lock checksum does not match reviewed provenance")
    if sys.version.split()[0] != PYTHON_VERSION:
        raise RuntimeError(f"Python {PYTHON_VERSION} is required, got {sys.version.split()[0]}")


def source_feature_contract(item: Any) -> dict[str, Any]:
    registration, attribute, mount_prefix = "include_router", "router", ""
    closure = getattr(item.register_fn, "__closure__", None) or ()
    freevars = dict(zip(getattr(item.register_fn.__code__, "co_freevars", ()), (cell.cell_contents for cell in closure), strict=True))
    if "prefix" in freevars:
        registration, mount_prefix = "mount_app", freevars["prefix"]
    if "attr_name" in freevars:
        attribute = freevars["attr_name"]
    return feature(
        item.name,
        item.module_path,
        tuple(item.path_prefixes),
        suffixes=tuple(item.path_suffixes),
        registration=registration,
        attribute=attribute,
        mount_prefix=mount_prefix,
        persistent_stub=bool(item.persistent_swagger_stub),
    )


def validate_feature_definitions(features: Any) -> list[dict[str, Any]]:
    actual = [source_feature_contract(item) for item in features]
    names = [item["name"] for item in actual]
    modules = [item["module"] for item in actual]
    if len(names) != len(set(names)) or len(modules) != len(set(modules)):
        raise RuntimeError("duplicate lazy feature definition")
    expected = list(EXPECTED_LAZY_FEATURES)
    if actual != expected:
        added = sorted(set(names) - {item["name"] for item in expected})
        removed = sorted({item["name"] for item in expected} - set(names))
        raise RuntimeError(f"lazy feature definitions differ from reviewed source contract: added={added} removed={removed}")
    return actual


def parameter_contract(route: Any) -> dict[str, list[str]]:
    path_parameters: set[str] = set()
    query_parameters: set[str] = set()
    visited: set[int] = set()

    def walk(dependant: Any) -> None:
        if dependant is None or id(dependant) in visited:
            return
        visited.add(id(dependant))
        path_parameters.update(field.name for field in getattr(dependant, "path_params", ()))
        query_parameters.update(field.name for field in getattr(dependant, "query_params", ()))
        for dependency in getattr(dependant, "dependencies", ()):
            walk(dependency)

    walk(getattr(route, "dependant", None))
    return {"path_parameters": sorted(path_parameters), "query_parameters": sorted(query_parameters)}


def route_operations(routes: Any, prefix: str = "", *, include_hidden: bool = True) -> dict[tuple[str, str], dict[str, list[str]]]:
    from fastapi.routing import APIRoute

    operations: dict[tuple[str, str], dict[str, list[str]]] = {}
    for route in routes:
        if not include_hidden and not getattr(route, "include_in_schema", True):
            continue
        # FastAPI only documents APIRoute instances. Plain Starlette routes (for
        # example v1.104.0's legacy MCP SSE transport routes appended to the
        # mounted MCP app) are runtime transports, never OpenAPI operations.
        if not include_hidden and not isinstance(route, APIRoute):
            continue
        route_path = getattr(route, "path_format", None) or getattr(route, "path", None)
        for method in sorted(getattr(route, "methods", ()) or ()):
            method = method.upper()
            if not route_path or method not in HTTP_METHODS:
                continue
            path = prefix.rstrip("/") + (route_path if route_path.startswith("/") else "/" + route_path)
            key, contract = (method, path), parameter_contract(route)
            prior = operations.get(key)
            if prior is not None and prior != contract:
                raise RuntimeError(f"conflicting duplicate route contract for {method} {path}")
            operations[key] = contract
    return operations


def schema_route_operations(schema: dict[str, Any]) -> dict[tuple[str, str], dict[str, list[str]]]:
    operations: dict[tuple[str, str], dict[str, list[str]]] = {}
    for path, item in schema.get("paths", {}).items():
        inherited = item.get("parameters", []) if isinstance(item, dict) else []
        for method, operation in item.items():
            upper = method.upper()
            if upper not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            path_parameters: set[str] = set()
            query_parameters: set[str] = set()
            for parameter in [*inherited, *operation.get("parameters", [])]:
                if not isinstance(parameter, dict):
                    continue
                if parameter.get("in") == "path":
                    path_parameters.add(parameter.get("name"))
                elif parameter.get("in") == "query":
                    query_parameters.add(parameter.get("name"))
            operations[(upper, path)] = {
                "path_parameters": sorted(path_parameters),
                "query_parameters": sorted(query_parameters),
            }
    return operations


def schema_operations(schema: dict[str, Any]) -> set[tuple[str, str]]:
    return set(schema_route_operations(schema))


def prefix_schema(schema: dict[str, Any], prefix: str) -> dict[str, Any]:
    return {
        **schema,
        "paths": {prefix.rstrip("/") + path: item for path, item in schema.get("paths", {}).items()},
    }


def merge_schema(destination: dict[str, Any], fragment: dict[str, Any]) -> None:
    for path, item in fragment.get("paths", {}).items():
        existing = destination.setdefault("paths", {}).get(path)
        if existing is not None and existing != item:
            raise RuntimeError(f"mounted application OpenAPI conflicts at {path}")
        destination["paths"][path] = item
    schemas = destination.setdefault("components", {}).setdefault("schemas", {})
    for name, definition in fragment.get("components", {}).get("schemas", {}).items():
        if name in schemas and schemas[name] != definition:
            raise RuntimeError(f"mounted application schema conflicts for {name}")
        schemas[name] = definition


def direct_register_features(app: Any, features: Any, importer: Any = importlib.import_module) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[tuple[str, set[tuple[str, str]]]]]:
    from fastapi.openapi.utils import get_openapi
    from starlette.routing import Mount

    evidence: list[dict[str, Any]] = []
    mounted_fragments: list[dict[str, Any]] = []
    feature_operations: list[tuple[str, set[tuple[str, str]]]] = []
    for item, contract in zip(features, EXPECTED_LAZY_FEATURES, strict=True):
        try:
            module = importer(item.module_path)
        except Exception as exc:
            raise RuntimeError(f"lazy feature {item.name!r} failed to import") from exc
        before = len(app.routes)
        try:
            item.register_fn(app, module)
        except Exception as exc:
            raise RuntimeError(f"lazy feature {item.name!r} failed to register") from exc
        added = list(app.routes[before:])
        if not added:
            raise RuntimeError(f"lazy feature {item.name!r} registered zero routes")

        prefix = ""
        live_routes = added
        if contract["registration"] == "mount_app":
            mounts = [route for route in added if isinstance(route, Mount)]
            mounted_app = getattr(module, contract["attribute"], None)
            if len(mounts) != 1 or mounts[0].path != contract["mount_prefix"] or mounts[0].app is not mounted_app:
                raise RuntimeError(f"lazy feature {item.name!r} mounted application extraction failed")
            live_routes = getattr(mounted_app, "routes", None)
            if live_routes is None:
                raise RuntimeError(f"lazy feature {item.name!r} mounted application extraction failed")
            prefix = contract["mount_prefix"]

        live = route_operations(live_routes, prefix)
        if not live:
            raise RuntimeError(f"lazy feature {item.name!r} has zero live HTTP routes")
        visible = route_operations(live_routes, prefix, include_hidden=False)
        fragment = get_openapi(title=app.title, version=app.version, routes=live_routes)
        if prefix:
            fragment = prefix_schema(fragment, prefix)
            mounted_fragments.append(fragment)
        generated_contracts = schema_route_operations(fragment)
        generated = set(generated_contracts)
        if generated_contracts != visible:
            raise RuntimeError(f"lazy feature {item.name!r} live routes disagree with its generated OpenAPI")
        feature_operations.append((item.name, generated))
        evidence.append({**contract, "live_operation_count": len(live), "openapi_operation_count": len(generated)})
        if hasattr(app.state, "lazy_loaded"):
            app.state.lazy_loaded.add(item.module_path)
    return evidence, mounted_fragments, feature_operations


def verify_public_organization_update_route(root: Path) -> None:
    """Require the organization update route to be a single public decorator.

    An upstream regression back to include_in_schema=False would silently drop
    the route from generated OpenAPI, so fail instead of exporting without it.
    """
    source_path = root / ORGANIZATION_UPDATE_ROUTE_SOURCE
    tree = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(ORGANIZATION_UPDATE_ROUTE_SOURCE))
    matches = 0
    for node in tree.body:
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for decorator in node.decorator_list:
            if not isinstance(decorator, ast.Call) or not isinstance(decorator.func, ast.Attribute) or not decorator.args:
                continue
            method, path_arg = decorator.func.attr.upper(), decorator.args[0]
            if method != ORGANIZATION_UPDATE_ROUTE[0] or not isinstance(path_arg, ast.Constant) or path_arg.value != ORGANIZATION_UPDATE_ROUTE[1]:
                continue
            include = next((kw.value for kw in decorator.keywords if kw.arg == "include_in_schema"), None)
            if include is not None and not (isinstance(include, ast.Constant) and include.value is True):
                raise RuntimeError("reviewed organization update route is hidden from OpenAPI")
            if "organization_id" not in {arg.arg for arg in node.args.args}:
                raise RuntimeError("reviewed organization update route path parameter changed")
            matches += 1
    if matches != 1:
        raise RuntimeError(f"expected exactly one reviewed organization update route, found {matches}")


def canonical_write(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")


def export(root: Path, openapi_output: Path, supplemental_output: Path) -> None:
    verify_checkout(root)
    os.chdir(root)
    sys.path.insert(0, str(root))
    proxy_server = importlib.import_module("litellm.proxy.proxy_server")
    lazy = importlib.import_module("litellm.proxy._lazy_features")
    app = proxy_server.app
    if not hasattr(app.state, "lazy_loaded"):
        app.state.lazy_loaded, app.state.lazy_locks = set(), {}

    validate_feature_definitions(lazy.LAZY_FEATURES)
    evidence, mounted_fragments, feature_operations = direct_register_features(app, lazy.LAZY_FEATURES)

    # All feature routers are live. Disable the committed lazy snapshot injector
    # explicitly so it cannot supply or mask any exported path.
    lazy.inject_lazy_stubs = lambda schema, *args, **kwargs: schema
    app.openapi_schema = None
    schema = app.openapi()
    for fragment in mounted_fragments:
        merge_schema(schema, fragment)

    generated = schema_operations(schema)
    for name, operations in feature_operations:
        missing = sorted(operations - generated)
        if missing:
            raise RuntimeError(f"lazy feature {name!r} routes are absent from final generated OpenAPI: {missing}")
    verify_public_organization_update_route(root)
    expected_org_update = {"path_parameters": ["organization_id"], "query_parameters": []}
    if schema_route_operations(schema).get(ORGANIZATION_UPDATE_ROUTE) != expected_org_update:
        raise RuntimeError("organization update route is absent from generated OpenAPI or its parameters changed")

    # Recreate visible feature keys from direct route evidence by requiring the
    # final schema operation total to include every per-feature generated key.
    # direct_register_features already compared exact keys/contracts per feature.
    if not generated:
        raise RuntimeError("generated OpenAPI contains zero operations")

    canonical_write(openapi_output, schema)
    canonical_write(supplemental_output, {
        "schema_version": 2,
        "upstream_commit": UPSTREAM_COMMIT,
        "lazy_features": evidence,
        "routes": [],
    })


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", required=True, type=Path)
    parser.add_argument("--openapi-output", required=True, type=Path)
    parser.add_argument("--supplemental-output", required=True, type=Path)
    args = parser.parse_args()
    try:
        export(args.source.resolve(), args.openapi_output.resolve(), args.supplemental_output.resolve())
    except Exception as exc:
        print(f"contract export failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
