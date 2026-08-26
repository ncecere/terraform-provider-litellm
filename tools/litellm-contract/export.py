#!/usr/bin/env python3
"""Export the reviewed LiteLLM API contract.

This program must run from an exact LiteLLM source checkout with dependencies
installed from that checkout's uv.lock. It deliberately imports every lazy
router used by the provider instead of accepting LiteLLM's warn-and-skip lazy
loader behavior.
"""

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
UPSTREAM_TAG = "v1.98.0"
UPSTREAM_COMMIT = "d8f71d7bdbd7c9873d98293f83d64c6db72847e6"
PYTHON_VERSION = "3.12.14"
UV_LOCK_SHA256 = "a7cc57875c67de85bbae0f82b834f31fc9d0c029073ef29e0883787a31a985e8"
REQUIRED_LAZY_FEATURES = (
    "access_groups",
    "agents",
    "guardrails",
    "mcp_management",
    "prompts",
    "search_tools",
)
HIDDEN_ROUTE_SOURCE = Path("litellm/proxy/management_endpoints/organization_endpoints.py")
HIDDEN_ROUTE = ("PATCH", "/v2/organization/{organization_id}")
HTTP_METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE"}


def run_git(root: Path, *args: str) -> str:
    completed = subprocess.run(
        ["git", "-C", str(root), *args],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    return completed.stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_checkout(root: Path) -> None:
    if run_git(root, "rev-parse", "HEAD") != UPSTREAM_COMMIT:
        raise RuntimeError(f"source checkout is not exact commit {UPSTREAM_COMMIT}")
    tags = set(run_git(root, "tag", "--points-at", "HEAD").splitlines())
    if UPSTREAM_TAG not in tags:
        raise RuntimeError(f"commit is not tagged {UPSTREAM_TAG}")
    if run_git(root, "status", "--porcelain", "--untracked-files=no"):
        raise RuntimeError("source checkout has tracked modifications")
    lock = root / "uv.lock"
    if sha256(lock) != UV_LOCK_SHA256:
        raise RuntimeError("uv.lock checksum does not match reviewed provenance")
    if sys.version.split()[0] != PYTHON_VERSION:
        raise RuntimeError(f"Python {PYTHON_VERSION} is required, got {sys.version.split()[0]}")


def parameter_contract(route: Any) -> dict[str, list[str]]:
    dependant = getattr(route, "dependant", None)
    return {
        "path_parameters": sorted({field.name for field in getattr(dependant, "path_params", ())}),
        "query_parameters": sorted({field.name for field in getattr(dependant, "query_params", ())}),
    }


def walk_fastapi_routes(app: Any) -> dict[tuple[str, str], dict[str, list[str]]]:
    operations: dict[tuple[str, str], dict[str, list[str]]] = {}
    for route in app.routes:
        path = getattr(route, "path", None)
        for method in sorted(getattr(route, "methods", ()) or ()):
            method = method.upper()
            if not path or method not in HTTP_METHODS:
                continue
            key = (method, path)
            contract = parameter_contract(route)
            prior = operations.get(key)
            if prior is not None and prior != contract:
                raise RuntimeError(f"conflicting duplicate route contract for {method} {path}")
            operations[key] = contract
    return operations


def parse_hidden_route(root: Path) -> dict[str, Any]:
    """Extract one reviewed include_in_schema=False route from one pinned file.

    This is intentionally not a general Python route scanner. Broad AST
    inference would create a second, unreliable OpenAPI generator.
    """
    source_path = root / HIDDEN_ROUTE_SOURCE
    tree = ast.parse(source_path.read_text(encoding="utf-8"), filename=str(HIDDEN_ROUTE_SOURCE))
    matches: list[dict[str, Any]] = []
    for node in tree.body:
        if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            continue
        for decorator in node.decorator_list:
            if not isinstance(decorator, ast.Call) or not isinstance(decorator.func, ast.Attribute):
                continue
            method = decorator.func.attr.upper()
            if method != HIDDEN_ROUTE[0] or not decorator.args:
                continue
            path_arg = decorator.args[0]
            if not isinstance(path_arg, ast.Constant) or path_arg.value != HIDDEN_ROUTE[1]:
                continue
            include = next((kw.value for kw in decorator.keywords if kw.arg == "include_in_schema"), None)
            if not isinstance(include, ast.Constant) or include.value is not False:
                raise RuntimeError("reviewed hidden organization route is no longer hidden")
            arguments = {arg.arg for arg in node.args.args}
            path_parameters = ["organization_id"]
            if not set(path_parameters).issubset(arguments):
                raise RuntimeError("reviewed hidden organization route path parameter changed")
            matches.append(
                {
                    "method": method,
                    "path": HIDDEN_ROUTE[1],
                    "path_parameters": path_parameters,
                    "query_parameters": [],
                    "evidence": str(HIDDEN_ROUTE_SOURCE),
                    "reason": "include_in_schema=false upstream route used by organization updates",
                }
            )
    if len(matches) != 1:
        raise RuntimeError(f"expected exactly one reviewed hidden route, found {len(matches)}")
    return matches[0]


def canonical_write(path: Path, value: Any) -> None:
    encoded = json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n"
    path.write_text(encoded, encoding="utf-8", newline="\n")


def export(root: Path, openapi_output: Path, supplemental_output: Path) -> None:
    verify_checkout(root)
    os.chdir(root)
    sys.path.insert(0, str(root))

    proxy_server = importlib.import_module("litellm.proxy.proxy_server")
    lazy = importlib.import_module("litellm.proxy._lazy_features")
    app = proxy_server.app
    if not hasattr(app.state, "lazy_loaded"):
        app.state.lazy_loaded = set()
        app.state.lazy_locks = {}
    features = {feature.name: feature for feature in lazy.LAZY_FEATURES}
    missing = sorted(set(REQUIRED_LAZY_FEATURES) - set(features))
    if missing:
        raise RuntimeError(f"required lazy feature metadata missing: {', '.join(missing)}")

    reviewed_route_ids: set[int] = set()
    for name in REQUIRED_LAZY_FEATURES:
        feature = features[name]
        try:
            module = importlib.import_module(feature.module_path)
            before = len(app.routes)
            feature.register_fn(app, module)
            if len(app.routes) <= before:
                raise RuntimeError("router registration added no routes")
            reviewed_route_ids.update(id(route) for route in app.routes[before:])
            app.state.lazy_loaded.add(feature.module_path)
        except Exception as exc:
            raise RuntimeError(f"required lazy feature {name!r} failed to import/register") from exc

    app.openapi_schema = None
    route_contracts = walk_fastapi_routes(app)
    required_prefixes = {
        "agents": "/v1/agents",
        "guardrails": "/guardrails",
        "mcp_management": "/v1/mcp/server",
        "prompts": "/prompts",
        "search_tools": "/search_tools",
        "access_groups": "/v1/access_group",
    }
    for name, prefix in required_prefixes.items():
        if not any(path == prefix or path.startswith(prefix + "/") for _, path in route_contracts):
            raise RuntimeError(f"required lazy feature {name!r} registered no reviewed routes")

    schema = app.openapi()
    schema_paths = schema.get("paths", {})
    for route in app.routes:
        if id(route) not in reviewed_route_ids or not getattr(route, "include_in_schema", True):
            continue
        path = getattr(route, "path", "")
        for method in getattr(route, "methods", ()) or ():
            if method in HTTP_METHODS and method.lower() not in schema_paths.get(path, {}):
                raise RuntimeError(f"schema omitted reviewed lazy route {method} {path}")

    hidden = parse_hidden_route(root)
    registered_hidden = route_contracts.get(HIDDEN_ROUTE)
    expected_hidden = {
        "path_parameters": hidden["path_parameters"],
        "query_parameters": hidden["query_parameters"],
    }
    if registered_hidden != expected_hidden:
        raise RuntimeError("registered hidden organization route disagrees with bounded AST evidence")

    canonical_write(openapi_output, schema)
    canonical_write(
        supplemental_output,
        {
            "schema_version": 1,
            "upstream_commit": UPSTREAM_COMMIT,
            "routes": [hidden],
        },
    )


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
