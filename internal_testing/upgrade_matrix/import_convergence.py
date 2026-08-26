#!/usr/bin/env python3
"""Classify a reviewed post-import convergence plan without exposing values."""
from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any


class ImportConvergenceError(ValueError):
    pass


def classify_import_convergence(value: dict[str, Any], target: str) -> str:
    target_actions = None
    for change in value.get("resource_changes", []):
        address = change.get("address")
        actions = change.get("change", {}).get("actions", [])
        if address == target:
            if target_actions is not None:
                raise ImportConvergenceError("target appeared more than once")
            target_actions = actions
        elif actions not in ([], ["no-op"]):
            raise ImportConvergenceError("unrelated resource action")
    if target_actions not in (["no-op"], ["update"]):
        raise ImportConvergenceError("target action was not no-op or update")

    changed_outputs = {
        name: change.get("actions", [])
        for name, change in value.get("output_changes", {}).items()
        if change.get("actions", []) not in ([], ["no-op"])
    }
    allowed_stale_output = {"matrix_import_id": ["delete"]}
    if changed_outputs not in ({}, allowed_stale_output):
        raise ImportConvergenceError("output action was not reviewed")

    if target_actions == ["update"]:
        return "target-update"
    if changed_outputs == allowed_stale_output:
        return "stale-output-delete"
    raise ImportConvergenceError("detailed plan contained no reviewed change")


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        return 2
    value = json.loads(Path(argv[1]).read_text(encoding="utf-8"))
    try:
        result = classify_import_convergence(value, argv[2])
    except (ImportConvergenceError, TypeError, AttributeError):
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
