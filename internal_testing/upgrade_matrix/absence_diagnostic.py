#!/usr/bin/env python3
"""Recognize only reviewed provider/API evidence of authoritative remote absence."""
from __future__ import annotations

import re
import sys
from pathlib import Path

MAX_EVIDENCE_BYTES = 2 * 1024 * 1024
RESOURCE_TYPE_RE = re.compile(r"litellm_[a-z_]+")
BOUNDED_API_ABSENCE_RE = re.compile(
    r"unable to read [a-z0-9 _-]+: api request failed with status "
    r"(?:400|404); response detail\s+omitted"
)
FORBIDDEN = (
    "invalid address",
    "configuration is invalid",
    "variables not allowed",
    "provider configuration",
    "failed to load plugin schemas",
)
CORE_ABSENCE = (
    "cannot import non-existent remote object",
    "remote object does not exist",
)
ENDPOINT_EXACT = {
    "litellm_budget": (
        "unable to read budget: budget import read response did not contain "
        "exactly one budget"
    ),
    "litellm_fallback": (
        "error: fallback import read error litellm returned http status 404 "
        "while attempting to read during import the fallback."
    ),
}


def is_authoritative_not_found(text: str, resource_type: str) -> bool:
    if len(text.encode()) > MAX_EVIDENCE_BYTES:
        return False
    if RESOURCE_TYPE_RE.fullmatch(resource_type) is None:
        return False
    lower = text.lower()
    if any(value in lower for value in FORBIDDEN):
        return False
    normalized = " ".join(lower.split())
    return (
        any(value in lower for value in CORE_ABSENCE)
        or BOUNDED_API_ABSENCE_RE.search(lower) is not None
        or (
            resource_type in ENDPOINT_EXACT
            and ENDPOINT_EXACT[resource_type] in normalized
        )
    )


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        return 2
    evidence = Path(argv[1]).read_text(encoding="utf-8", errors="replace")
    return 0 if is_authoritative_not_found(evidence, argv[2]) else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
