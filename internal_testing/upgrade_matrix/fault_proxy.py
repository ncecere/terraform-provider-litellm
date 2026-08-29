#!/usr/bin/env python3
"""One-shot, loopback-only deterministic fault proxy for recovery scenarios."""
import argparse
import http.client
import json
import os
import stat
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

MAX_BODY = 4 * 1024 * 1024
ALLOWED = {("POST", "/model/new"), ("POST", "/team/new")}


def exclusive_json(path: Path, value: dict) -> None:
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    try:
        os.write(fd, json.dumps(value, separators=(",", ":")).encode())
        os.fsync(fd)
    finally:
        os.close(fd)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--endpoint", required=True)
    parser.add_argument("--port-file", required=True)
    parser.add_argument("--stats-file", required=True)
    args = parser.parse_args()
    endpoint = ("POST", args.endpoint)
    if endpoint not in ALLOWED:
        raise SystemExit("endpoint is not allowlisted")
    stats = {"attempted": 0, "faulted_before_forward": 0, "target_forwarded": 0, "other_forwarded": 0}

    class Handler(BaseHTTPRequestHandler):
        server_version = "controlled-fault-proxy"
        sys_version = ""

        def log_message(self, *_):
            return

        def _handle(self):
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0 or length > MAX_BODY:
                self.send_error(413)
                return
            body = self.rfile.read(length)
            route = self.path.split("?", 1)[0]
            if (self.command, route) == endpoint and stats["attempted"] == 0:
                stats["attempted"] = 1
                stats["faulted_before_forward"] = 1
                exclusive_json(Path(args.stats_file), stats)
                payload = b'{"error":{"code":"issue210_controlled_precommit_fault"}}'
                self.send_response(503)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return
            if (self.command, route) == endpoint:
                stats["target_forwarded"] += 1
            else:
                stats["other_forwarded"] += 1
            connection = http.client.HTTPConnection("127.0.0.1", 4000, timeout=15)
            headers = {key: value for key, value in self.headers.items() if key.lower() not in {"host", "content-length", "connection"}}
            try:
                connection.request(self.command, self.path, body=body, headers=headers)
                response = connection.getresponse()
                payload = response.read(MAX_BODY + 1)
                if len(payload) > MAX_BODY:
                    self.send_error(502)
                    return
                self.send_response(response.status)
                for key, value in response.getheaders():
                    if key.lower() not in {"transfer-encoding", "connection", "content-length"}:
                        self.send_header(key, value)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
            finally:
                connection.close()

        do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _handle

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    server.daemon_threads = True
    exclusive_json(Path(args.port_file), {"port": server.server_port})
    server.serve_forever(poll_interval=0.1)


if __name__ == "__main__":
    main()
