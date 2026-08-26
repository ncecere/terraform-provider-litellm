#!/usr/bin/env python3
"""Update only generated review metadata after a successful exact export."""

from __future__ import annotations

import hashlib
import json
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / "internal/contract/manifest.json"
GOLDEN = ROOT / "internal/contractapi/testdata/provider-operations.golden.json"
HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}


def checksum(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main() -> None:
    manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
    openapi_path = ROOT / manifest["openapi"]["path"]
    supplemental_path = ROOT / manifest["supplemental"]["path"]
    openapi = json.loads(openapi_path.read_text(encoding="utf-8"))
    supplemental = json.loads(supplemental_path.read_text(encoding="utf-8"))
    extracted = subprocess.run(
        ["go", "run", "./internal/cmd/contract-check", "-extract"],
        cwd=ROOT,
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    ).stdout
    operations = json.loads(extracted)

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
    MANIFEST.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    GOLDEN.write_text(json.dumps(operations, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
