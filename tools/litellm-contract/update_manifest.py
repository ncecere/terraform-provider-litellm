#!/usr/bin/env python3
"""Generate manifest metadata and provider-operation golden in a staging root.

This script never edits reviewed-pins.json or the reviewed operation
classification. A reviewer must update those separately before contract-update
can install changed generated artifacts.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
from pathlib import Path

HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}


def checksum(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="staging artifact root")
    parser.add_argument("--tool-root", type=Path, default=Path.cwd(), help="provider checkout containing the Go command")
    args = parser.parse_args()
    root = args.root.resolve()
    tool_root = args.tool_root.resolve()

    manifest_path = root / "internal/contract/manifest.json"
    golden_path = root / "internal/contractapi/testdata/provider-operations.golden.json"
    pins_path = root / "internal/contract/reviewed-pins.json"
    classification_path = root / "internal/contract/reviewed-operation-classification.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    pins = json.loads(pins_path.read_text(encoding="utf-8"))
    classification = json.loads(classification_path.read_text(encoding="utf-8"))
    openapi_path = root / manifest["openapi"]["path"]
    supplemental_path = root / manifest["supplemental"]["path"]
    openapi = json.loads(openapi_path.read_text(encoding="utf-8"))
    supplemental = json.loads(supplemental_path.read_text(encoding="utf-8"))

    extracted = subprocess.run(
        ["go", "run", "./internal/cmd/contract-check", "-extract", "-root", str(root)],
        cwd=tool_root,
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    ).stdout
    operations = json.loads(extracted)
    golden_path.parent.mkdir(parents=True, exist_ok=True)
    golden_path.write_text(json.dumps(operations, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    category_dispositions = {item["id"]: item["disposition"] for item in pins["categories"]}
    disposition_counts = {"unsupported_durable": 0, "excluded_non_durable": 0}
    for operation in classification["operations"]:
        disposition_counts[category_dispositions[operation["category"]]] += 1

    manifest["openapi"].update(
        sha256=checksum(openapi_path),
        path_count=len(openapi["paths"]),
        operation_count=sum(method in HTTP_METHODS for item in openapi["paths"].values() for method in item),
    )
    manifest["supplemental"].update(
        sha256=checksum(supplemental_path),
        route_count=len(supplemental["routes"]),
    )
    manifest["provider_operations"] = operations
    manifest["provider_golden"] = {
        "path": str(golden_path.relative_to(root)),
        "sha256": checksum(golden_path),
        "operation_count": len(operations),
    }
    manifest["classification"] = {
        "path": str(classification_path.relative_to(root)),
        "sha256": checksum(classification_path),
        "operation_count": len(classification["operations"]),
        **disposition_counts,
    }
    manifest.pop("unsupported_durable_operations", None)
    manifest.pop("excluded_non_durable_categories", None)
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
