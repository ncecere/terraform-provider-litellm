#!/usr/bin/env python3
"""Validate Prompt's exact fail-closed, non-authoritative post-destroy import error."""
from __future__ import annotations

import argparse
from pathlib import Path

MAX_BYTES = 2 * 1024 * 1024
TITLE = "Error: Prompt Read Error"
DETAIL = (
    "Unable to read and validate the scoped prompt. "
    "Response and request details were omitted."
)


def validate(path: Path) -> None:
    raw = path.read_bytes()
    if len(raw) > MAX_BYTES:
        raise ValueError("diagnostic exceeds its private bound")
    text = raw.decode("utf-8", errors="replace")
    error_titles = [line.strip() for line in text.splitlines() if line.strip().startswith("Error:")]
    if error_titles != [TITLE]:
        raise ValueError("diagnostic title is not the exact single allowlisted error")
    normalized = " ".join(text.split())
    if normalized.count(DETAIL) != 1:
        raise ValueError("diagnostic detail is not the exact fail-closed Prompt read error")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("evidence", type=Path)
    args = parser.parse_args()
    try:
        validate(args.evidence)
    except (OSError, ValueError) as exc:
        parser.error(str(exc))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
