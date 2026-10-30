#!/usr/bin/env python3
"""Disposable Terraform assembly against synthetic AWS and LiteLLM HTTP servers.

No real AWS credentials, production gateways, or remote Terraform state are used.
Usage: python3 -I external_rotation_mock.py --terraform PATH --provider PATH
"""

import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading
from urllib.parse import parse_qs, urlsplit


SECRET_ARN = "arn:aws:secretsmanager:us-west-2:123456789012:secret:service-accounts/key-example-AbCdEf"
OLD = "sk-synthetic-external-old-not-a-real-key"
NEW = "sk-synthetic-external-new-not-a-real-key"


class Backend:
    def __init__(self):
        self.current = OLD
        self.version = "synthetic-v1"
        self.mismatch = False
        self.aws_calls = []
        self.gateway_calls = []
        self.rows = {OLD: self.row(OLD), NEW: self.row(NEW)}

    @staticmethod
    def row(token):
        return {
            "key_alias": "key-example",
            "team_id": "team-example",
            "project_id": "project-example",
            "metadata": {"service_account_id": "sa-example", "existing": "preserved"},
            "models": ["example-model"],
            "allowed_routes": ["llm_api_routes"],
            "blocked": False,
            "max_budget": 100,
            "status": "active",
        }


def handlers(backend):
    class Common(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def reply(self, value, status=200):
            body = json.dumps(value).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    class AWS(Common):
        def do_POST(self):
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            operation = self.headers.get("X-Amz-Target", "").split(".")[-1]
            backend.aws_calls.append(operation)
            if operation != "GetSecretValue" or payload.get("SecretId") != SECRET_ARN:
                self.reply({"__type": "ResourceNotFoundException"}, 400)
                return
            self.reply({
                "ARN": SECRET_ARN,
                "Name": "service-accounts/key-example",
                "VersionId": backend.version,
                "VersionStages": ["AWSCURRENT"],
                "SecretString": backend.current,
            })

    class Gateway(Common):
        def target(self, identifier):
            for token in backend.rows:
                if identifier in (token, hashlib.sha256(token.encode()).hexdigest()):
                    return token
            return None

        def do_GET(self):
            parsed = urlsplit(self.path)
            backend.gateway_calls.append(("GET", parsed.path))
            identifier = parse_qs(parsed.query).get("key", [""])[0]
            token = self.target(identifier)
            if parsed.path != "/key/info" or token is None:
                self.reply({"error": "not found"}, 404)
                return
            row = dict(backend.rows[token])
            if backend.mismatch:
                row["team_id"] = "wrong-team"
            # LiteLLM removes info.token and echoes the exact request in key.
            self.reply({"key": identifier, "info": row})

        def do_POST(self):
            backend.gateway_calls.append(("POST", self.path))
            payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.path != "/key/update":
                self.reply({"error": "generation/deletion forbidden in this test"}, 405)
                return
            token = self.target(payload.pop("key", ""))
            if token != backend.current:
                self.reply({"error": "wrong current key"}, 400)
                return
            backend.rows[token].update(payload)
            self.reply({"key": hashlib.sha256(token.encode()).hexdigest()})

    return AWS, Gateway


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--terraform", required=True)
    parser.add_argument("--provider", required=True, help="Locally built provider executable")
    args = parser.parse_args()
    backend = Backend()
    aws_handler, gateway_handler = handlers(backend)
    aws = ThreadingHTTPServer(("127.0.0.1", 0), aws_handler)
    gateway = ThreadingHTTPServer(("127.0.0.1", 0), gateway_handler)
    for server in (aws, gateway):
        threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with tempfile.TemporaryDirectory(prefix="litellm-external-rotation-mock-") as temporary:
            work = Path(temporary)
            provider_dir = work / "provider"
            provider_dir.mkdir()
            binary = provider_dir / "terraform-provider-litellm"
            binary.symlink_to(Path(args.provider).resolve())
            cli = work / "terraform.rc"
            cli.write_text('provider_installation {\n  dev_overrides { "ncecere/litellm" = %s }\n  direct {}\n}\n' % json.dumps(str(provider_dir)))
            (work / "main.tf").write_text('''terraform {
  required_providers {
    litellm = { source = "ncecere/litellm" }
  }
}
provider "litellm" {
  api_base = "http://127.0.0.1:%s"
  api_key = "sk-synthetic-admin-not-real"
}
variable "budget" {
  type = number
  default = 100
}
resource "litellm_key" "example" {
  service_account_id = "sa-example"
  key_alias = "key-example"
  team_id = "team-example"
  project_id = "project-example"
  external_rotation = { secret_arn = "%s" }
  max_budget = var.budget
  models = ["example-model"]
  allowed_routes = ["llm_api_routes"]
  blocked = false
}
''' % (gateway.server_port, SECRET_ARN))
            environment = {
                key: value for key, value in os.environ.items()
                if not key.startswith(("AWS_", "TF_", "LITELLM_"))
            }
            environment.update({
                "TF_CLI_CONFIG_FILE": str(cli),
                "AWS_ACCESS_KEY_ID": "synthetic-access-key",
                "AWS_SECRET_ACCESS_KEY": "synthetic-secret-key",
                "AWS_REGION": "us-west-2",
                "AWS_ENDPOINT_URL_SECRETS_MANAGER": "http://127.0.0.1:" + str(aws.server_port),
                "AWS_EC2_METADATA_DISABLED": "true",
                "AWS_CONFIG_FILE": str(work / "absent-config"),
                "AWS_SHARED_CREDENTIALS_FILE": str(work / "absent-credentials"),
                "GOMAXPROCS": "2",
            })

            def run(*arguments, expect_success=True):
                result = subprocess.run(
                    [str(Path(args.terraform).resolve()), *arguments], cwd=work,
                    env=environment, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                    text=True, timeout=120,
                )
                if expect_success and result.returncode:
                    # Only synthetic credentials are used; still avoid printing
                    # raw Terraform diagnostics to keep the test safe by example.
                    (work / "last-failure.log").write_text(result.stdout)
                    raise AssertionError("Terraform command failed: " + arguments[0])
                return result

            def plan(budget=100):
                run("plan", "-input=false", "-no-color", "-var=budget=" + str(budget), "-out=test.tfplan")
                document = json.loads(run("show", "-json", "test.tfplan").stdout)
                return document

            plan()
            run("apply", "-input=false", "-no-color", "test.tfplan")
            state = json.loads(run("state", "pull").stdout)
            attrs = state["resources"][0]["instances"][0]["attributes"]
            assert attrs["key"] == OLD
            assert attrs["service_account_id"] == "sa-example"
            assert not any(path in ("/key/generate", "/key/service-account/generate", "/key/delete") for _, path in backend.gateway_calls)
            print("PASS: missing state adopts the exact existing key without generation")

            backend.current, backend.version = NEW, "synthetic-v2"
            backend.rows.pop(OLD)
            document = plan()
            planned = document["planned_values"]["root_module"]["resources"][0]["values"]
            assert planned["key"] == NEW
            assert planned["id"] == "sha256:" + hashlib.sha256(NEW.encode()).hexdigest()
            assert all("create" not in change["change"]["actions"] and "delete" not in change["change"]["actions"] for change in document.get("resource_changes", []))
            run("apply", "-input=false", "-no-color", "test.tfplan")
            print("PASS: normal refresh follows rotation after the old key disappears")

            plan(120)
            run("apply", "-input=false", "-no-color", "test.tfplan")
            assert backend.rows[NEW]["max_budget"] == 120
            print("PASS: budget update targets the verified current key")

            plan(130)
            backend.version = "synthetic-v3"
            before = len([call for call in backend.gateway_calls if call[0] == "POST"])
            result = run("apply", "-input=false", "-no-color", "test.tfplan", expect_success=False)
            assert result.returncode != 0
            assert len([call for call in backend.gateway_calls if call[0] == "POST"]) == before
            assert OLD not in result.stdout and NEW not in result.stdout
            print("PASS: changed secret version between plan/apply stops before mutation")

            backend.mismatch = True
            before = len(backend.gateway_calls)
            result = run("plan", "-input=false", "-no-color", expect_success=False)
            assert result.returncode != 0
            assert not any(method == "POST" for method, _ in backend.gateway_calls[before:])
            assert OLD not in result.stdout and NEW not in result.stdout
            print("PASS: wrong-team key fails safely without credential diagnostics")
            backend.mismatch = False

            run("destroy", "-auto-approve", "-input=false", "-no-color", "-var=budget=120")
            assert NEW in backend.rows
            assert not any(path == "/key/delete" for _, path in backend.gateway_calls)
            print("PASS: removal forgets the binding without deleting the external key")
            assert set(backend.aws_calls) == {"GetSecretValue"}
            print("All assembly checks passed against synthetic local services only.")
    finally:
        for server in (aws, gateway):
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
