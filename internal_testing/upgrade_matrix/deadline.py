#!/usr/bin/env python3
"""Execute one command with a wall deadline and process-group cleanup."""
import argparse
import os
import signal
import subprocess
import sys

parser = argparse.ArgumentParser()
parser.add_argument("--seconds", type=int, required=True)
parser.add_argument("command", nargs=argparse.REMAINDER)
args = parser.parse_args()
if not 1 <= args.seconds <= 900 or not args.command:
    raise SystemExit(2)
process = subprocess.Popen(args.command, stdin=subprocess.DEVNULL, start_new_session=True)
try:
    raise SystemExit(process.wait(timeout=args.seconds))
except subprocess.TimeoutExpired:
    os.killpg(process.pid, signal.SIGTERM)
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid, signal.SIGKILL)
        process.wait()
    print("command exceeded controlled deadline", file=sys.stderr)
    raise SystemExit(124)
