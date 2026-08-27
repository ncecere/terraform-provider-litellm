#!/usr/bin/env python3
"""Classify a reviewed post-import convergence plan without exposing values."""
from __future__ import annotations

import json
import re
import sys
from pathlib import Path
from typing import Any


class ImportConvergenceError(ValueError):
    pass


def parse_stale_outputs(value: str) -> set[str]:
    names = set(filter(None, value.split(",")))
    if any(re.fullmatch(r"[A-Za-z][A-Za-z0-9_]*", name) is None for name in names):
        raise ImportConvergenceError("stale output name was malformed")
    return {"matrix_import_id", *names}


def classify_import_convergence(
    value: dict[str, Any], target: str, allowed_stale_outputs: set[str]
) -> str:
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
    if any(
        name not in allowed_stale_outputs or actions != ["delete"]
        for name, actions in changed_outputs.items()
    ):
        raise ImportConvergenceError("output action was not reviewed")

    if target_actions == ["update"]:
        return "target-update"
    if changed_outputs:
        return "stale-output-delete"
    raise ImportConvergenceError("detailed plan contained no reviewed change")


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        return 2
    value = json.loads(Path(argv[1]).read_text(encoding="utf-8"))
    try:
        allowed = parse_stale_outputs(argv[3])
        result = classify_import_convergence(value, argv[2], allowed)
    except (ImportConvergenceError, TypeError, AttributeError):
        return 1
    print(result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
