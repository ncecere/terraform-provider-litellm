#!/usr/bin/env python3
"""Validate the exact pre-1.11 Agent imported-block representation failure."""
from __future__ import annotations

import argparse
from pathlib import Path

MAX_BYTES = 2 * 1024 * 1024
TITLE = "Error: Provider produced invalid plan"
DETAIL = (
    "planned an invalid value for litellm_agent.minimal.agent_card.provider: "
    "planned for existence but config wants absence."
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
        raise ValueError("diagnostic detail is not the exact imported-block representation failure")


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
