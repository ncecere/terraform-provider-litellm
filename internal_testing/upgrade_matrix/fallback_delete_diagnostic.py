#!/usr/bin/env python3
"""Validate the exact fallback deletion confirmation failure diagnostic."""
from __future__ import annotations

import argparse
from pathlib import Path

MAX_BYTES = 2 * 1024 * 1024
TITLE = "Error: Fallback Delete Unconfirmed"
DETAIL = (
    "LiteLLM's authoritative fallback GET remained present after the bounded DELETE confirmation. "
    "Terraform state was retained."
)


def validate(path: Path) -> None:
    raw = path.read_bytes()
    if len(raw) > MAX_BYTES:
        raise ValueError("diagnostic exceeds its private bound")
    text = raw.decode("utf-8", errors="replace")
    titles = [line.strip() for line in text.splitlines() if line.strip().startswith("Error:")]
    if titles != [TITLE]:
        raise ValueError("diagnostic is not the exact single fallback confirmation error")
    normalized = " ".join(text.split())
    if normalized.count(DETAIL) != 1:
        raise ValueError("diagnostic detail is not the exact confirmation-presence failure")
    rejected = (
        "command exceeded controlled deadline", "while attempting to delete the fallback",
        "could not delete the fallback", "timed out", " timeout", "cancelled", "canceled",
        "context deadline exceeded", "connection", "transport", "unexpected eof", " unavailable",
    )
    if any(value in normalized.lower() for value in rejected):
        raise ValueError("diagnostic contains an operational delete failure")


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
