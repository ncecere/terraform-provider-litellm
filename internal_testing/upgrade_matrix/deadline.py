#!/usr/bin/env python3
"""Execute one command with bounded output, a wall deadline, and group cleanup."""
import argparse
import fcntl
import hashlib
import hmac
import json
import os
import selectors
import signal
import stat
import subprocess
import sys
import time

parser = argparse.ArgumentParser()
parser.add_argument("--seconds", type=int, required=True)
parser.add_argument("--max-output", type=int, default=2 * 1024 * 1024)
parser.add_argument("command", nargs=argparse.REMAINDER)
args = parser.parse_args()
if not 1 <= args.seconds <= 900 or not 1024 <= args.max_output <= 10 * 1024 * 1024 or not args.command:
    raise SystemExit(2)

process = subprocess.Popen(
    args.command,
    stdin=subprocess.DEVNULL,
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
    start_new_session=True,
)
assert process.stdout is not None
selector = selectors.DefaultSelector()
selector.register(process.stdout, selectors.EVENT_READ)
deadline = time.monotonic() + args.seconds
captured = bytearray()
failure = None

while selector.get_map():
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        failure = "command exceeded controlled deadline"
        break
    events = selector.select(min(remaining, 1.0))
    if not events and process.poll() is not None:
        # One final nonblocking iteration drains EOF.
        events = selector.select(0)
        if not events:
            break
    for key, _ in events:
        chunk = os.read(key.fd, 65536)
        if not chunk:
            selector.unregister(key.fileobj)
            continue
        captured.extend(chunk)
        if len(captured) > args.max_output:
            failure = "command exceeded controlled output bound"
            break
    if failure:
        break

if failure:
    os.killpg(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
else:
    try:
        process.wait(timeout=max(0.0, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        failure = "command exceeded controlled deadline"
        os.killpg(process.pid, signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()

if failure:
    returncode = 124 if "deadline" in failure else 125
    captured.extend(("\n" + failure).encode())
else:
    returncode = process.returncode

# The bounded executor is the command-evidence supervisor. It stores no argv,
# output, path, URL, identifier, or credential: only domain-separated digests
# and controlled session bindings. The report builder accepts records only when
# they reference one of these receipts from the same nonce-bound session.
ledger = os.environ.get("MATRIX_COMMAND_LEDGER")
if ledger:
    required = {
        "run_nonce": os.environ.get("MATRIX_RUN_NONCE", ""),
        "cli_lane": os.environ.get("MATRIX_CLI_LANE", ""),
        "candidate_commit": os.environ.get("MATRIX_CANDIDATE_COMMIT", ""),
        "provider_sha256": os.environ.get("MATRIX_PROVIDER_SHA256", ""),
        "provider_schema_sha256": os.environ.get("MATRIX_PROVIDER_SCHEMA_SHA256", ""),
        "harness_sha256": os.environ.get("MATRIX_HARNESS_SHA256", ""),
        "matrix_sha256": os.environ.get("MATRIX_MATRIX_SHA256", ""),
    }
    if any(not value for value in required.values()):
        print("command evidence session binding is incomplete", file=sys.stderr)
        raise SystemExit(126)
    command_encoded = json.dumps(args.command, separators=(",", ":"), ensure_ascii=False).encode()
    result_encoded = returncode.to_bytes(4, "big", signed=True) + bytes(captured)
    receipt = {
        "record_type": "command",
        **required,
        "command_sha256": hashlib.sha256(b"issue210-command-v1\0" + command_encoded).hexdigest(),
        "result_sha256": hashlib.sha256(b"issue210-result-v1\0" + result_encoded).hexdigest(),
        "exit_code": returncode,
        "output_bytes": len(captured),
    }
    key_path = os.environ.get("MATRIX_LEDGER_KEY_FILE", "")
    key_fd = os.open(key_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        key_info = os.fstat(key_fd)
        key = os.read(key_fd, 33)
        if not stat.S_ISREG(key_info.st_mode) or key_info.st_nlink != 1 or len(key) != 32:
            raise OSError("unsafe evidence signing key")
    finally:
        os.close(key_fd)
    canonical = json.dumps(receipt, sort_keys=True, separators=(",", ":")).encode()
    receipt["receipt_hmac"] = "hmac-sha256:" + hmac.new(key, canonical, hashlib.sha256).hexdigest()
    flags = os.O_WRONLY | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(ledger, flags)
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        info = os.fstat(descriptor)
        if not os.path.isfile(ledger) or info.st_nlink != 1 or info.st_size > 8 * 1024 * 1024:
            raise OSError("unsafe command evidence ledger")
        os.write(descriptor, (json.dumps(receipt, sort_keys=True, separators=(",", ":")) + "\n").encode())
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

if failure:
    print(failure, file=sys.stderr)
    raise SystemExit(returncode)
os.write(sys.stdout.fileno(), captured)
raise SystemExit(returncode)
