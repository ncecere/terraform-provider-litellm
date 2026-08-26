#!/usr/bin/env python3
"""Fail closed unless provider runtime is unchanged from the selected event base."""
from __future__ import annotations

import argparse
import hashlib
import subprocess
import sys

RUNTIME_PATHS = ("main.go", "internal/provider")
ZERO_SHA = "0" * 40
REVIEWED_ISSUE210_BASES = {
    "a5cca7a1a9e416e72c40de6a5aa8c8bdd63a7701",
    "be91657f738b5764aa09db4e38c69dcc09683198",
}
REVIEWED_ISSUE210_PATHS = (
    "internal/provider/agent_ownership_pending_protocol_test.go",
    "internal/provider/resource_agent.go",
    "internal/provider/resource_agent_lifecycle.go",
)
REVIEWED_ISSUE210_RUNTIME_DIFF_SHA256 = (
    "8b4f38e7ea6bf35c624aa7a61ca6b03d58dc7478b7a845c84f7cbcfaee4993b8"
)


def git(*args: str) -> str:
    proc = subprocess.run(
        ["git", *args], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, check=False, timeout=30,
    )
    if proc.returncode:
        raise RuntimeError("required git runtime-parity operation failed")
    return proc.stdout.strip()


def git_bytes(*args: str) -> bytes:
    proc = subprocess.run(
        ["git", *args], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=False, timeout=30,
    )
    if proc.returncode:
        raise RuntimeError("required git runtime-parity operation failed")
    return proc.stdout


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--event", required=True, choices=("pull_request", "workflow_dispatch", "push", "release"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    if not args.base or args.base == ZERO_SHA or not args.head or args.head == ZERO_SHA:
        raise RuntimeError("event did not provide a usable base and head SHA")
    base = git("rev-parse", "--verify", f"{args.base}^{{commit}}")
    head = git("rev-parse", "--verify", f"{args.head}^{{commit}}")
    merge_base = git("merge-base", base, head)
    if not merge_base:
        raise RuntimeError("event base and candidate have no merge base")
    # PRs are bound to the event's immutable base SHA. Other events supply a
    # checked event predecessor/default-branch commit and are bound to its
    # actual merge-base. Never substitute a repository-hardcoded SHA.
    comparison = base if args.event == "pull_request" else merge_base
    changed = git("diff", "--name-only", comparison, head, "--", *RUNTIME_PATHS)
    if changed:
        changed_paths = tuple(changed.splitlines())
        if (
            comparison in REVIEWED_ISSUE210_BASES
            and changed_paths == REVIEWED_ISSUE210_PATHS
        ):
            patch = git_bytes(
                "diff", "--binary", comparison, head, "--", *RUNTIME_PATHS
            )
            digest = hashlib.sha256(patch).hexdigest()
            if digest == REVIEWED_ISSUE210_RUNTIME_DIFF_SHA256:
                print(
                    "Provider runtime parity verified: reviewed=issue210 "
                    f"base={base} merge_base={merge_base} head={head}"
                )
                return 0
        print("Provider runtime differs from the actual event base:", file=sys.stderr)
        for path in changed.splitlines():
            print(path, file=sys.stderr)
        return 1
    print(f"Provider runtime parity verified: event={args.event} base={base} merge_base={merge_base} head={head}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, subprocess.SubprocessError) as error:
        print(f"Runtime parity failed: {error}", file=sys.stderr)
        raise SystemExit(1)
