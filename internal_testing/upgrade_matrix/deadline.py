#!/usr/bin/env python3
"""Execute one command with bounded output, a wall deadline, and group cleanup."""
import argparse
import os
import selectors
import signal
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
    print(failure, file=sys.stderr)
    raise SystemExit(124 if "deadline" in failure else 125)
os.write(sys.stdout.fileno(), captured)
raise SystemExit(process.returncode)
