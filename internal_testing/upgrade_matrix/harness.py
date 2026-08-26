#!/usr/bin/env python3
"""Fail-closed assembly and safety primitives for the upgrade matrix.

This program deliberately never relays child-process output. Terraform/OpenTofu
state, plans, logs, remote IDs, credentials, and URLs stay in mode-0700 scratch
space and are removed by the caller's cleanup trap. The only durable result is
an allowlisted JSON summary.
"""
from __future__ import annotations

import argparse
import contextlib
import errno
import fcntl
import hashlib
import hmac
import json
import os
import platform
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import zipfile
from pathlib import Path
from typing import Iterable

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
MATRIX_PATH = HERE / "matrix.json"
TOOLS_PATH = HERE / "tools.lock.json"
MAX_CAPTURE = 1024 * 1024
MAX_DOWNLOAD = 200 * 1024 * 1024
DOWNLOAD_RETRIES = 3
DOWNLOAD_DEADLINE_SECONDS = 120
REPORT_SCHEMA_VERSION = 2
CATEGORIES = (
    "inventory", "format", "schema", "resource_coverage", "upgrade",
    "lifecycle", "import", "drift", "replacement", "failure_recovery",
    "data_source", "optional_feature", "documentation",
)
EXECUTION_CATEGORIES = set(CATEGORIES) - {"inventory", "format", "schema"}
STATUSES = {"passed", "failed", "skipped"}
MODES = {"assembly", "destructive-local", "remote-preflight"}
ALLOWED_RESULT_KEYS = {"schema_version", "mode", "summary", "scenarios", "provenance"}
ALLOWED_SCENARIO_KEYS = {
    "name", "subject", "category", "status", "evidence_code", "reason",
    "diagnostic_code",
}
ALLOWED_PROVENANCE_KEYS = {
    "cli_product", "cli_version", "cli_executable_sha256",
    "provider_executable_sha256", "provider_schema_sha256",
    "previous_signature_sha256", "previous_archive_sha256",
    "previous_executable_sha256", "previous_provider_schema_sha256",
    "previous_manifest_sha256", "previous_signing_fingerprint",
}
EVIDENCE_CODES = {
    "inventory-validated", "format-validated", "provider-schema-validated",
    "apply-refresh-plan-destroy", "apply-refresh-plan", "import-refresh-apply-plan-detach",
    "upgrade-refresh-plan", "upgrade-reviewed-migration", "replacement-plan-apply", "fault-retry-converged",
    "api-unavailable", "enterprise-unavailable", "cli-feature-unavailable",
    "previous-release-unavailable", "documentation-validated", "remote-mutation-disabled",
}
DIAGNOSTIC_TITLE_CODES = {
    "Client Error": "model-create-error",
    "Team Member Create Error": "team-member-create-error",
}
DIAGNOSTIC_CODES = set(DIAGNOSTIC_TITLE_CODES.values())
URL_RE = re.compile(r"(?i)(?:\b(?:https?|postgres(?:ql)?|file)://\S+|\b[a-z0-9.-]+:\d{2,5}(?:/\S*)?)")
UUID_RE = re.compile(r"(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b")
ABS_PATH_RE = re.compile(r"(?:^|[\s\"'])/(?:Users|home|tmp|var|private|workspace|github)/[^\s\"']+")
SECRET_RE = re.compile(
    r"(?i)(?:sk-[A-Za-z0-9_-]{4,}|bearer\s+\S+|"
    r"(?:api[_-]?key|client[_-]?secret|password|authorization|cookie)\s*[=:]\s*\S+)"
)
PROTECTED_KEY_RE = re.compile(r"(?i)(?:password|client[_-]?secret|api[_-]?key|authorization|body|url|path|remote[_-]?id|private)")
ID_RE = re.compile(r"(?i)\b(?:id|token|key|url|state|plan|log|body|path)\s*[=:]\s*\S+")


class HarnessError(RuntimeError):
    pass


def load_json(path: Path) -> dict:
    with path.open(encoding="utf-8") as handle:
        return json.load(handle)


def version_tuple(value: str) -> tuple[int, ...]:
    match = re.search(r"\d+(?:\.\d+)+", value)
    if not match:
        raise HarnessError("tool version is not numeric")
    return tuple(int(part) for part in match.group(0).split("."))


def supports_optional_111(version: str) -> bool:
    return version_tuple(version) >= (1, 11, 0)


def selected_cli_version(cli: str) -> str:
    proc = subprocess.run(
        [cli, "version"], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, timeout=30, check=False,
    )
    output = (proc.stdout + proc.stderr)[:16384]
    if proc.returncode != 0:
        raise HarnessError("selected CLI version check failed")
    match = re.search(r"\d+\.\d+\.\d+", output)
    if not match:
        raise HarnessError("selected CLI did not report a semantic version")
    return match.group(0)


def redact(text: str) -> str:
    text = URL_RE.sub("[REDACTED_URL]", text)
    text = SECRET_RE.sub("[REDACTED_SECRET]", text)
    return ID_RE.sub("[REDACTED_VALUE]", text)


def diagnostic_titles(text: str) -> list[str]:
    """Extract diagnostic titles only; never include detail/body text."""
    titles: list[str] = []
    try:
        value = json.loads(text)
        values = value if isinstance(value, list) else value.get("diagnostics", [])
        for item in values:
            title = item.get("summary") if isinstance(item, dict) else None
            if isinstance(title, str) and title and not URL_RE.search(title) and not SECRET_RE.search(title):
                titles.append(redact(title)[:160])
    except (json.JSONDecodeError, AttributeError):
        pass
    return sorted(set(titles))[:20]


def safe_environment(extra: dict[str, str] | None = None) -> dict[str, str]:
    allowed = ("PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR")
    env = {key: os.environ[key] for key in allowed if key in os.environ}
    env.update({"TF_IN_AUTOMATION": "1", "TF_INPUT": "0", "TF_CLI_ARGS": "-no-color"})
    if extra:
        env.update(extra)
    return env


def safe_run(command: list[str], cwd: Path, env: dict[str, str] | None = None) -> tuple[int, list[str]]:
    proc = subprocess.run(
        command,
        cwd=cwd,
        env=safe_environment(env),
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=False,
        timeout=300,
        check=False,
    )
    raw = proc.stdout + proc.stderr
    if len(raw) > MAX_CAPTURE:
        raise HarnessError("child output exceeded the bounded capture limit")
    return proc.returncode, diagnostic_titles(raw.decode("utf-8", "replace"))


def require_regular_file(path: Path, *, executable: bool = False) -> os.stat_result:
    try:
        info = path.lstat()
    except FileNotFoundError as error:
        raise HarnessError("required regular file is missing") from error
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise HarnessError("path must be a regular file and not a symlink")
    if info.st_nlink != 1:
        raise HarnessError("hard-linked files are not accepted")
    if executable and not info.st_mode & stat.S_IXUSR:
        raise HarnessError("verified executable is not executable")
    return info


def require_private_directory(path: Path) -> None:
    info = path.lstat()
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        raise HarnessError("private directory is not a real directory")
    if stat.S_IMODE(info.st_mode) & 0o077:
        raise HarnessError("private directory permissions are too broad")


def hash_file(path: Path) -> str:
    before = require_regular_file(path)
    digest = hashlib.sha256()
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(path, flags)
    try:
        with os.fdopen(descriptor, "rb", closefd=False) as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(chunk)
        after = os.fstat(descriptor)
    finally:
        os.close(descriptor)
    if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (
        after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns
    ):
        raise HarnessError("file changed while it was being verified")
    return digest.hexdigest()


def safe_hmac(value: bytes, key: bytes) -> str:
    return "hmac-sha256:" + hmac.new(key, value, hashlib.sha256).hexdigest()


def compare_private_ids(old_path: Path, new_path: Path, key: bytes) -> bool:
    """Compare remote IDs without returning or persisting either ID."""
    def fingerprint(path: Path) -> bytes:
        value = path.read_bytes().strip()
        if not value:
            raise HarnessError("replacement ID capture is empty")
        return bytes.fromhex(safe_hmac(value, key).split(":", 1)[1])
    return hmac.compare_digest(fingerprint(old_path), fingerprint(new_path))


def import_cleanup_commands(addresses: Iterable[str]) -> list[list[str]]:
    commands = []
    for address in addresses:
        if not re.fullmatch(r"[A-Za-z0-9_]+\.[A-Za-z0-9_]+(?:\[[^\n]+\])?", address):
            raise HarnessError("unsafe Terraform address in cleanup inventory")
        commands.append(["state", "rm", address])
    return commands


def account_skips(results: list[dict], allowed: set[str]) -> None:
    for result in results:
        if result.get("status") != "skipped":
            continue
        reason = result.get("reason")
        if reason not in allowed:
            raise HarnessError("unexplained scenario skip")


def require_cleanup_success(statuses: Iterable[int]) -> None:
    if any(status != 0 for status in statuses):
        raise HarnessError("one or more cleanup operations failed")


def provider_types(source: str, kind: str) -> set[str]:
    prefix = "resource_" if kind == "resource" else "datasource_"
    result: set[str] = set()
    for path in (ROOT / "internal" / "provider").glob(f"{prefix}*.go"):
        text = path.read_text(encoding="utf-8")
        result.update("litellm_" + name for name in re.findall(r'req\.ProviderTypeName \+ "_([a-z_]+)"', text))
    return result


def check_inventory(matrix: dict) -> None:
    resources = matrix["resources"]
    expected_resources = {item["type"] for item in resources}
    expected_data_sources = set(matrix["data_sources"])
    if len(resources) != 24 or len(expected_resources) != 24:
        raise HarnessError("resource inventory must contain exactly 24 unique types")
    if len(matrix["data_sources"]) != 35 or len(expected_data_sources) != 35:
        raise HarnessError("data-source inventory must contain exactly 35 unique types")
    if expected_resources != provider_types("", "resource"):
        raise HarnessError("resource inventory differs from provider registration")
    if expected_data_sources != provider_types("", "data_source"):
        raise HarnessError("data-source inventory differs from provider registration")
    actions = sorted(item["type"] for item in resources if item.get("action"))
    introduced = {item["type"] for item in resources if item.get("introduced_after_previous")}
    if matrix.get("non_importable_resources") != [] or actions != sorted(matrix.get("action_resources", [])):
        raise HarnessError("importable/action resource accounting is incomplete")
    if introduced != {"litellm_jwt_key_mapping"}:
        raise HarnessError("post-v2.0.1 resource accounting is incomplete")
    for item in resources:
        for required in ("fixture", "address", "import_expression", "lane", "action"):
            if required not in item:
                raise HarnessError(f"resource matrix entry lacks {required}")
        doc = ROOT / "docs" / "resources" / f"{item['type'].removeprefix('litellm_')}.md"
        if not doc.is_file() or "## Import" not in doc.read_text(encoding="utf-8"):
            raise HarnessError("an importable resource lacks import documentation")
        for fixture in item["fixture"] + item.get("import_fixture", []):
            if not (ROOT / "internal_testing" / "resources" / fixture).is_file():
                raise HarnessError("resource matrix references a missing fixture")
    expected_counts = {
        "resource_coverage": 24, "upgrade": 24, "lifecycle": 24, "import": 24,
        "drift": 24, "replacement": 3, "failure_recovery": 2,
        "data_source": 35, "documentation": 3,
    }
    if matrix.get("scenario_counts") != expected_counts:
        raise HarnessError("scenario count contract changed without explicit accounting")
    for scenario in matrix.get("replacement_scenarios", []):
        if scenario.get("name") == "jwt_claim_pair_identity" and scenario.get("minimum_cli") != "1.11.0":
            raise HarnessError("JWT replacement does not have an exact CLI feature gate")
        if (
            scenario.get("compare") != "hmac-sha256"
            or scenario.get("expected_actions") != ["create", "delete"]
            or not scenario.get("dependency_address")
            or not scenario.get("post_replacement_no_drift")
        ):
            raise HarnessError("replacement evidence contract is incomplete")
        if not (HERE / scenario["fixture"]).is_file():
            raise HarnessError("replacement fixture is missing")
    for scenario in matrix.get("failure_recovery_scenarios", []):
        if not scenario.get("cleanup_required") or not scenario.get("expected_diagnostic_title"):
            raise HarnessError("failure-recovery accounting is incomplete")
        if not (HERE / scenario["fixture"]).is_file():
            raise HarnessError("failure-recovery fixture is missing")
    account_skips([], set(matrix["allowed_skip_reasons"]))


def check_release_contract(tools: dict) -> None:
    previous = tools["previous_provider"]
    if previous["version"] != "2.0.1" or len(previous["archives"]) < 2:
        raise HarnessError("previous provider release is not pinned")
    key = previous.get("signing_key", {})
    key_path = HERE / key.get("file", "")
    if (
        key.get("fingerprint") != "C753834A70062246C92CEF56F0A1AEC231353F8B"
        or key.get("key_id") != "F0A1AEC231353F8B"
        or not key_path.is_file()
        or hash_file(key_path) != key.get("sha256")
    ):
        raise HarnessError("release signing key or fingerprint is not the exact pin")
    digests = [
        *previous.get("registry_metadata_sha256", {}).values(), previous.get("checksums_file_sha256"),
        previous.get("signature_sha256"), previous.get("manifest_sha256"),
        previous.get("schema_sha256"),
    ]
    for archive in previous["archives"].values():
        digests.extend((archive.get("sha256"), archive.get("executable_sha256")))
    if any(not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value) for value in digests):
        raise HarnessError("release checksum is malformed")
    for product, minimum in (("terraform", (1, 0, 0)), ("opentofu", (1, 6, 0))):
        versions = tools[product]
        if min(version_tuple(value) for value in versions) < minimum:
            raise HarnessError("tool baseline is below the supported floor")
        if not any(supports_optional_111(value) for value in versions):
            raise HarnessError("tool matrix lacks a >=1.11 optional-feature lane")
        for metadata in versions.values():
            for archive in metadata["archives"].values():
                if not re.fullmatch(r"[0-9a-f]{64}", archive["sha256"]):
                    raise HarnessError("tool checksum is malformed")


def make_provider_bundle(directory: Path, provider_binary: Path) -> tuple[Path, str]:
    """Copy one verified executable into a new private dev-override directory."""
    source_digest = hash_file(provider_binary)
    source_info = require_regular_file(provider_binary)
    if not source_info.st_mode & stat.S_IXUSR:
        raise HarnessError("current provider binary is not executable")
    bundle = directory / ("provider-dev-" + secrets.token_hex(8))
    bundle.mkdir(mode=0o700)
    target = bundle / "terraform-provider-litellm"
    source_fd = os.open(provider_binary, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    target_fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o700)
    try:
        with os.fdopen(source_fd, "rb", closefd=False) as source, os.fdopen(target_fd, "wb", closefd=False) as output:
            shutil.copyfileobj(source, output, 1024 * 1024)
            output.flush()
            os.fsync(target_fd)
    finally:
        os.close(source_fd)
        os.close(target_fd)
    if hash_file(target) != source_digest:
        raise HarnessError("provider dev-override copy digest mismatch")
    entries = list(bundle.iterdir())
    if entries != [target] or not target.is_file() or target.is_symlink():
        raise HarnessError("provider dev override must contain exactly one regular executable")
    return bundle, source_digest


def make_cli_config(directory: Path, provider_binary: Path) -> Path:
    provider_dir, _ = make_provider_bundle(directory, provider_binary)
    config = directory / "terraformrc"
    descriptor = os.open(config, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
    encoded = (
        'provider_installation {\n  dev_overrides {\n'
        f'    "registry.terraform.io/ncecere/litellm" = {json.dumps(str(provider_dir))}\n'
        "  }\n}\n"
    ).encode()
    try:
        os.write(descriptor, encoded)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    return config


def provider_schema_fingerprint(cli: str, provider_binary: Path) -> tuple[str, str]:
    with tempfile.TemporaryDirectory(prefix="litellm-provider-schema-") as raw:
        module = Path(raw)
        module.chmod(0o700)
        shutil.copy2(ROOT / "internal_testing" / "provider.tf", module / "provider.tf")
        shutil.copy2(ROOT / "internal_testing" / "variables.tf", module / "variables.tf")
        config = make_cli_config(module, provider_binary)
        proc = subprocess.run(
            [cli, "providers", "schema", "-json"], cwd=module,
            env=safe_environment({"TF_CLI_CONFIG_FILE": str(config)}),
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            timeout=60, check=False,
        )
        if proc.returncode or len(proc.stdout) > MAX_CAPTURE or len(proc.stderr) > MAX_CAPTURE:
            raise HarnessError("current provider protocol/schema query failed")
        try:
            schema = json.loads(proc.stdout)
            selected = schema["provider_schemas"]["registry.terraform.io/ncecere/litellm"]
        except (json.JSONDecodeError, KeyError, TypeError) as error:
            raise HarnessError("current provider schema response was invalid") from error
        canonical = json.dumps(selected, sort_keys=True, separators=(",", ":")).encode()
        return hash_file(provider_binary), hashlib.sha256(canonical).hexdigest()


def validate_resource_modules(matrix: dict, cli: str, provider_binary: Path) -> list[dict]:
    results: list[dict] = []
    supports_111 = supports_optional_111(selected_cli_version(cli))
    with tempfile.TemporaryDirectory(prefix="litellm-matrix-") as raw:
        scratch = Path(raw)
        scratch.chmod(0o700)
        cli_config = make_cli_config(scratch, provider_binary)
        for item in matrix["resources"]:
            if item.get("lane") == "requires-1.11" and not supports_111:
                results.append({
                    "name": f"schema:{item['type']}", "subject": item["type"],
                    "category": "schema", "status": "skipped",
                    "reason": "cli-version-below-1.11", "evidence_code": "cli-feature-unavailable",
                })
                continue
            module = scratch / item["type"]
            module.mkdir(mode=0o700)
            shutil.copy2(ROOT / "internal_testing" / "provider.tf", module / "provider.tf")
            shutil.copy2(ROOT / "internal_testing" / "variables.tf", module / "variables.tf")
            for index, fixture in enumerate(item["fixture"]):
                shutil.copy2(ROOT / "internal_testing" / "resources" / fixture, module / f"fixture_{index}.tf")
            code, titles = safe_run([cli, "fmt", "-check", "."], module, {"TF_CLI_CONFIG_FILE": str(cli_config)})
            status = "passed" if code == 0 else "failed"
            if code == 0:
                code, titles = safe_run([cli, "validate", "-json"], module, {"TF_CLI_CONFIG_FILE": str(cli_config)})
                status = "passed" if code == 0 else "failed"
            results.append({"name": f"schema:{item['type']}", "subject": item["type"], "category": "schema", "status": status, "evidence_code": "provider-schema-validated"})
    return results


def validate_optional_111(cli: str, provider_binary: Path) -> list[dict]:
    version = selected_cli_version(cli)
    if not supports_optional_111(version):
        return [{
            "name": "schema:optional-1.11",
            "subject": "optional-1.11",
            "category": "schema",
            "status": "skipped",
            "reason": "cli-version-below-1.11",
            "evidence_code": "cli-feature-unavailable",
        }]
    with tempfile.TemporaryDirectory(prefix="litellm-optional-") as raw:
        module = Path(raw)
        module.chmod(0o700)
        shutil.copy2(ROOT / "internal_testing" / "provider.tf", module / "provider.tf")
        shutil.copy2(ROOT / "internal_testing" / "variables.tf", module / "variables.tf")
        shutil.copy2(ROOT / "internal_testing" / "resources" / "key_write_only.tf", module / "key_write_only.tf")
        shutil.copy2(ROOT / "internal_testing" / "resources" / "send_invite_email.tf", module / "send_invite_email.tf")
        cli_config = make_cli_config(module, provider_binary)
        code, titles = safe_run([cli, "validate", "-json"], module, {"TF_CLI_CONFIG_FILE": str(cli_config)})
    return [{
        "name": "schema:optional-1.11",
        "subject": "optional-1.11",
        "category": "schema",
        "status": "passed" if code == 0 else "failed",
        "evidence_code": "provider-schema-validated",
    }]


def validate_examples(cli: str, provider_binary: Path) -> list[dict]:
    results: list[dict] = []
    supports_111 = supports_optional_111(selected_cli_version(cli))
    example_dirs = sorted(path.parent for path in (ROOT / "examples").glob("*/main.tf"))
    with tempfile.TemporaryDirectory(prefix="litellm-docs-") as raw:
        scratch = Path(raw)
        scratch.chmod(0o700)
        cli_config = make_cli_config(scratch, provider_binary)
        for source in example_dirs:
            if source.name == "jwt-key-mapping" and not supports_111:
                results.append({
                    "name": f"format:{source.name}", "subject": source.name,
                    "category": "format", "status": "skipped",
                    "reason": "cli-version-below-1.11", "evidence_code": "cli-feature-unavailable",
                })
                continue
            module = scratch / source.name
            shutil.copytree(source, module)
            env = {
                "TF_CLI_CONFIG_FILE": str(cli_config),
                "LITELLM_API_BASE": "http://127.0.0.1:1",
                "LITELLM_API_KEY": "matrix-placeholder",
            }
            variables = "\n".join(path.read_text(encoding="utf-8") for path in module.glob("*.tf"))
            for name in re.findall(r'variable\s+"([A-Za-z0-9_]+)"', variables):
                if name == "insecure_skip_verify":
                    continue
                env[f"TF_VAR_{name}"] = "http://127.0.0.1:1" if name.endswith(("_base", "_endpoint")) else "matrix-placeholder"
            code, titles = safe_run([cli, "validate", "-json"], module, env)
            # Data-source examples necessarily contact LiteLLM during plan. All
            # resource-only registry examples must also assemble a no-refresh plan.
            if code == 0 and source.name != "data-sources":
                code, titles = safe_run([cli, "plan", "-refresh=false", "-lock=false", "-out=example.tfplan"], module, env)
            results.append({
                "name": f"format:{source.name}",
                "subject": source.name,
                "category": "format",
                "status": "passed" if code == 0 else "failed",
                "evidence_code": "format-validated",
            })
    return results


def check_format(cli: str) -> None:
    # Terraform 1.0 accepts one directory per fmt invocation.
    for directory in ("examples", "internal_testing"):
        code, _ = safe_run([cli, "fmt", "-check", "-recursive", directory], ROOT)
        if code != 0:
            raise HarnessError("published or internal HCL is not formatted")


def credential_scan(paths: Iterable[Path]) -> None:
    for path in paths:
        require_regular_file(path)
        text = path.read_text(encoding="utf-8", errors="replace")
        if any(pattern.search(text) for pattern in (URL_RE, UUID_RE, ABS_PATH_RE, SECRET_RE, ID_RE)):
            raise HarnessError("result artifact failed secret/identity/location scan")
        try:
            value = json.loads(text)
        except json.JSONDecodeError as error:
            raise HarnessError("result artifact is not valid JSON") from error
        def walk(item: object) -> None:
            if isinstance(item, dict):
                for key, child in item.items():
                    if PROTECTED_KEY_RE.search(key):
                        raise HarnessError("result artifact contains a protected field name")
                    walk(child)
            elif isinstance(item, list):
                for child in item:
                    walk(child)
            elif isinstance(item, str) and item.startswith("hmac-") and not re.fullmatch(r"hmac-sha256:[0-9a-f]{64}", item):
                raise HarnessError("result artifact contains a non-safe HMAC value")
        walk(value)


def summarize_results(results: list[dict]) -> dict:
    summary = {category: {status: 0 for status in sorted(STATUSES)} for category in CATEGORIES}
    for result in results:
        summary[result["category"]][result["status"]] += 1
    return summary


def validate_report(report: dict) -> None:
    if set(report) != ALLOWED_RESULT_KEYS or report.get("schema_version") != REPORT_SCHEMA_VERSION:
        raise HarnessError("report top-level schema is invalid")
    if report.get("mode") not in MODES or not isinstance(report.get("scenarios"), list):
        raise HarnessError("report mode or scenario list is invalid")
    seen: set[tuple[str, str]] = set()
    for item in report["scenarios"]:
        if not isinstance(item, dict) or set(item) - ALLOWED_SCENARIO_KEYS:
            raise HarnessError("scenario schema contains an arbitrary field")
        required = {"name", "subject", "category", "status", "evidence_code"}
        if not required.issubset(item):
            raise HarnessError("scenario schema is incomplete")
        if item["category"] not in CATEGORIES or item["status"] not in STATUSES or item["evidence_code"] not in EVIDENCE_CODES:
            raise HarnessError("scenario contains an invalid enum")
        identity = (item["category"], item["subject"])
        if identity in seen:
            raise HarnessError("duplicate per-subject category result")
        seen.add(identity)
        if item["status"] == "skipped" and not item.get("reason"):
            raise HarnessError("scenario skip lacks an explicit reason")
        if item["status"] != "skipped" and "reason" in item:
            raise HarnessError("non-skipped scenario contains a reason")
        if "diagnostic_code" in item and item["diagnostic_code"] not in DIAGNOSTIC_CODES:
            raise HarnessError("scenario diagnostic is not allowlisted")
    if report.get("summary") != summarize_results(report["scenarios"]):
        raise HarnessError("report summary was not derived from emitted records")
    provenance = report.get("provenance")
    if not isinstance(provenance, dict) or set(provenance) - ALLOWED_PROVENANCE_KEYS:
        raise HarnessError("report provenance is invalid")
    for key, value in provenance.items():
        if key in {"cli_product", "cli_version"}:
            if not isinstance(value, str) or not re.fullmatch(r"[A-Za-z0-9.-]{1,32}", value):
                raise HarnessError("report provenance enum is invalid")
        elif key == "previous_signing_fingerprint":
            if value != "C753834A70062246C92CEF56F0A1AEC231353F8B":
                raise HarnessError("report signing fingerprint is invalid")
        elif not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value):
            raise HarnessError("report digest is invalid")


def write_report(path: Path, report: dict) -> None:
    validate_report(report)
    encoded = (json.dumps(report, indent=2, sort_keys=True) + "\n").encode()
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    require_private_directory(path.parent)
    temporary = path.parent / (".report-" + secrets.token_hex(16) + ".tmp")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(temporary, flags, 0o600)
    try:
        os.write(descriptor, encoded)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    try:
        credential_scan([temporary])
        # link(2) creates the final name atomically and fails if it exists. It
        # gives O_EXCL semantics without exposing a partially written report.
        os.link(temporary, path, follow_symlinks=False)
        directory_fd = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    except FileExistsError as error:
        raise HarnessError("report destination already exists") from error
    finally:
        temporary.unlink(missing_ok=True)
    credential_scan([path])


def assembly(args: argparse.Namespace) -> int:
    matrix = load_json(MATRIX_PATH)
    tools = load_json(TOOLS_PATH)
    check_inventory(matrix)
    check_release_contract(tools)
    check_format(args.cli)
    results: list[dict] = [{
        "name": "inventory:provider-types", "subject": "provider-types",
        "category": "inventory", "status": "passed", "evidence_code": "inventory-validated",
    }, {
        "name": "format:repository-hcl", "subject": "repository-hcl",
        "category": "format", "status": "passed", "evidence_code": "format-validated",
    }]
    provenance: dict[str, str] = {}
    if args.provider_binary:
        provider_binary = Path(args.provider_binary)
        binary_before, schema_digest = provider_schema_fingerprint(args.cli, provider_binary)
        provenance = {"provider_executable_sha256": binary_before, "provider_schema_sha256": schema_digest}
        results.extend(validate_resource_modules(matrix, args.cli, provider_binary))
        results.extend(validate_optional_111(args.cli, provider_binary))
        results.extend(validate_examples(args.cli, provider_binary))
        if hash_file(provider_binary) != binary_before:
            raise HarnessError("assembly modified the current provider binary")
        if any(item["status"] == "failed" for item in results):
            raise HarnessError("one or more assembly validations failed")
    report = {
        "schema_version": REPORT_SCHEMA_VERSION,
        "mode": "assembly",
        "summary": summarize_results(results),
        "scenarios": results,
        "provenance": provenance,
    }
    account_skips(results, set(matrix["allowed_skip_reasons"]))
    if args.report:
        write_report(Path(args.report), report)
    print(f"Assembly passed: emitted={len(results)} inventory/format/schema records; execution passes=0")
    return 0


def platform_key() -> str:
    system = {"Darwin":"darwin", "Linux":"linux"}.get(platform.system())
    machine = {"x86_64":"amd64", "arm64":"arm64", "aarch64":"arm64"}.get(platform.machine())
    if not system or not machine:
        raise HarnessError("unsupported tool-install platform")
    return f"{system}_{machine}"


def secure_directory(path: Path) -> Path:
    path = path.expanduser().absolute()
    current = Path(path.anchor)
    for part in path.parts[1:]:
        current = current / part
        if current.exists() or current.is_symlink():
            info = current.lstat()
            if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
                raise HarnessError("cache path contains a symlink or non-directory")
        else:
            current.mkdir(mode=0o700)
    path.chmod(0o700)
    require_private_directory(path)
    return path


@contextlib.contextmanager
def cache_lock(cache: Path):
    lock = cache / ".matrix-cache.lock"
    flags = os.O_RDWR | os.O_CREAT | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(lock, flags, 0o600)
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
            raise HarnessError("cache lock is not a private regular file")
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        yield
    finally:
        os.close(descriptor)


def download_verified(url: str, destination: Path, checksum: str, offline: bool) -> None:
    if destination.exists() or destination.is_symlink():
        if hash_file(destination) == checksum:
            return
        raise HarnessError("existing cache entry failed verification")
    if offline:
        raise HarnessError("verified cache entry is unavailable in offline mode")
    last_error: Exception | None = None
    deadline = time.monotonic() + DOWNLOAD_DEADLINE_SECONDS
    for attempt in range(DOWNLOAD_RETRIES):
        if time.monotonic() >= deadline:
            raise HarnessError("download exceeded the total wall deadline")
        partial = destination.parent / (".download-" + secrets.token_hex(16) + ".partial")
        descriptor = -1
        try:
            descriptor = os.open(
                partial,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
                0o600,
            )
            request = urllib.request.Request(url, headers={"User-Agent": "litellm-upgrade-matrix/2"})
            with urllib.request.urlopen(request, timeout=min(30, max(1, int(deadline - time.monotonic())))) as response:
                declared = response.headers.get("Content-Length")
                if declared and int(declared) > MAX_DOWNLOAD:
                    raise HarnessError("download exceeds the size bound")
                total = 0
                while True:
                    if time.monotonic() >= deadline:
                        raise HarnessError("download exceeded the total wall deadline")
                    chunk = response.read(min(1024 * 1024, MAX_DOWNLOAD + 1 - total))
                    if not chunk:
                        break
                    total += len(chunk)
                    if total > MAX_DOWNLOAD:
                        raise HarnessError("download exceeds the size bound")
                    os.write(descriptor, chunk)
            os.fsync(descriptor)
            os.close(descriptor)
            descriptor = -1
            if hash_file(partial) != checksum:
                raise HarnessError("download checksum mismatch")
            os.replace(partial, destination)
            if hash_file(destination) != checksum:
                raise HarnessError("cache entry changed after atomic installation")
            return
        except (OSError, urllib.error.URLError, HarnessError) as error:
            last_error = error
            if isinstance(error, HarnessError) and "checksum" in str(error):
                raise
            if attempt + 1 < DOWNLOAD_RETRIES:
                delay = 2 ** attempt
                if time.monotonic() + delay >= deadline:
                    raise HarnessError("download exceeded the total wall deadline") from error
                time.sleep(delay)
        finally:
            if descriptor >= 0:
                os.close(descriptor)
            partial.unlink(missing_ok=True)
    raise HarnessError("bounded download retries were exhausted") from last_error


def extract_executable(archive: Path, destination: Path, member: str, expected: str | None) -> Path:
    if expected is None:
        with zipfile.ZipFile(archive) as package:
            try:
                with package.open(member) as source:
                    digest = hashlib.sha256()
                    for chunk in iter(lambda: source.read(1024 * 1024), b""):
                        digest.update(chunk)
                    expected = digest.hexdigest()
            except KeyError as error:
                raise HarnessError("archive does not contain the exact executable") from error
    if destination.exists() or destination.is_symlink():
        require_private_directory(destination)
        executable = destination / member
        if len(list(destination.iterdir())) != 1 or hash_file(executable) != expected:
            raise HarnessError("cached extraction contains a sibling, decoy, or wrong executable")
        require_regular_file(executable, executable=True)
        return executable
    partial = destination.parent / (".extract-" + secrets.token_hex(16))
    partial.mkdir(mode=0o700)
    try:
        with zipfile.ZipFile(archive) as package:
            selected = None
            for info in package.infolist():
                mode = info.external_attr >> 16
                if info.filename.startswith("/") or ".." in Path(info.filename).parts:
                    raise HarnessError("archive contains an unsafe path")
                if stat.S_ISLNK(mode):
                    raise HarnessError("archive contains a symlink")
                if info.filename == member:
                    if selected is not None:
                        raise HarnessError("archive contains a duplicate executable member")
                    selected = info
            if selected is None or selected.is_dir():
                raise HarnessError("archive does not contain the exact executable")
            if selected.file_size > MAX_DOWNLOAD:
                raise HarnessError("archive executable exceeds the size bound")
            target = partial / member
            descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o700)
            try:
                with package.open(selected) as source:
                    while True:
                        chunk = source.read(1024 * 1024)
                        if not chunk:
                            break
                        os.write(descriptor, chunk)
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
        if hash_file(target) != expected:
            raise HarnessError("executable digest mismatch")
        os.replace(partial, destination)
    finally:
        if partial.exists():
            shutil.rmtree(partial)
    executable = destination / member
    require_regular_file(executable, executable=True)
    return executable


def install_tool(args: argparse.Namespace) -> int:
    tools = load_json(TOOLS_PATH)
    product = args.product
    metadata = tools[product].get(args.version)
    if metadata is None:
        raise HarnessError("requested tool version is not pinned")
    archive = metadata["archives"].get(platform_key())
    if archive is None:
        raise HarnessError("requested tool platform is not pinned")
    cache = secure_directory(Path(args.cache))
    with cache_lock(cache):
        zip_path = cache / archive["file"]
        download_verified(f"{metadata['base_url']}/{archive['file']}", zip_path, archive["sha256"], args.offline)
        destination = cache / product / args.version / platform_key()
        secure_directory(destination.parent)
        member = "terraform" if product == "terraform" else "tofu"
        executable = extract_executable(zip_path, destination, member, None)
        executable_digest = hash_file(executable)
    proc = subprocess.run(
        [str(executable), "version"], stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, timeout=30, check=False,
        env=safe_environment(),
    )
    output = (proc.stdout + proc.stderr)[:16384]
    expected_product = "Terraform" if product == "terraform" else "OpenTofu"
    if proc.returncode or expected_product not in output or selected_cli_version(str(executable)) != args.version:
        raise HarnessError("installed tool product/version did not match the exact pin")
    print(f"Verified {product} {args.version}; executable_sha256={executable_digest}")
    return 0


def verify_detached_signature(sums: Path, signature: Path, metadata: dict) -> None:
    executable = shutil.which("gpg") or shutil.which("gpg2")
    if not executable:
        raise HarnessError("GnuPG is required to verify the pinned release signature")
    with tempfile.TemporaryDirectory(prefix="litellm-gpg-") as raw:
        home = Path(raw)
        home.chmod(0o700)
        key_path = HERE / metadata["signing_key"]["file"]
        if hash_file(key_path) != metadata["signing_key"]["sha256"]:
            raise HarnessError("pinned release public key digest mismatch")
        imported = subprocess.run(
            [executable, "--homedir", str(home), "--batch", "--import", str(key_path)],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            timeout=30, check=False,
        )
        if imported.returncode:
            raise HarnessError("pinned release public key import failed")
        shown = subprocess.run(
            [executable, "--homedir", str(home), "--batch", "--with-colons", "--fingerprint"],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, timeout=30, check=False,
        )
        fingerprints = {line.split(":")[9] for line in shown.stdout.splitlines() if line.startswith("fpr:")}
        fingerprint = metadata["signing_key"]["fingerprint"]
        if shown.returncode or fingerprint not in fingerprints:
            raise HarnessError("release key fingerprint does not match the exact pin")
        verified = subprocess.run(
            [executable, "--homedir", str(home), "--batch", "--status-fd", "1", "--verify", str(signature), str(sums)],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, timeout=30, check=False,
        )
        valid = [line.split() for line in verified.stdout.splitlines() if line.startswith("[GNUPG:] VALIDSIG ")]
        if verified.returncode or len(valid) != 1 or valid[0][2] != fingerprint:
            raise HarnessError("release checksum signature is not valid for the exact pinned key")


def install_previous(args: argparse.Namespace) -> int:
    tools = load_json(TOOLS_PATH)["previous_provider"]
    key = platform_key()
    expected = tools["archives"].get(key)
    if not expected:
        raise HarnessError("previous provider platform is not pinned")
    cache = secure_directory(Path(args.cache))
    name = f"terraform-provider-litellm_2.0.1_{key}.zip"
    base = "https://github.com/ncecere/terraform-provider-litellm/releases/download/v2.0.1"
    archive = cache / name
    sums = cache / "terraform-provider-litellm_2.0.1_SHA256SUMS"
    signature = cache / "terraform-provider-litellm_2.0.1_SHA256SUMS.sig"
    manifest = cache / "terraform-provider-litellm_2.0.1_manifest.json"
    registry = cache / f"registry-v2.0.1-{key}.json"
    registry_url = f"https://registry.terraform.io/v1/providers/ncecere/litellm/2.0.1/download/{key.replace('_', '/')}"
    with cache_lock(cache):
        download_verified(registry_url, registry, tools["registry_metadata_sha256"][key], args.offline)
        registry_value = load_json(registry)
        registry_keys = registry_value.get("signing_keys", {}).get("gpg_public_keys", [])
        if len(registry_keys) != 1 or registry_keys[0].get("key_id") != tools["signing_key"]["key_id"]:
            raise HarnessError("Registry signing key identifier changed")
        armor = registry_keys[0].get("ascii_armor", "").rstrip() + "\n"
        if hashlib.sha256(armor.encode()).hexdigest() != tools["signing_key"]["sha256"]:
            raise HarnessError("Registry public key differs from the exact release key pin")
        download_verified(f"{base}/{sums.name}", sums, tools["checksums_file_sha256"], args.offline)
        download_verified(f"{base}/{signature.name}", signature, tools["signature_sha256"], args.offline)
        verify_detached_signature(sums, signature, tools)
        # Trust archive and Registry manifest only after authenticating SHA256SUMS.
        download_verified(f"{base}/{name}", archive, expected["sha256"], args.offline)
        download_verified(f"{base}/{manifest.name}", manifest, tools["manifest_sha256"], args.offline)
        listed = {
            filename: checksum
            for checksum, filename in (
                line.split(maxsplit=1)
                for line in sums.read_text(encoding="utf-8").splitlines()
                if len(line.split(maxsplit=1)) == 2
            )
        }
        if listed.get(name) != expected["sha256"] or listed.get(manifest.name) != tools["manifest_sha256"]:
            raise HarnessError("signed checksum manifest does not select pinned artifacts")
        manifest_value = load_json(manifest)
        if manifest_value != {"version": 1, "metadata": {"protocol_versions": ["6.0"]}}:
            raise HarnessError("published registry manifest changed unexpectedly")
        extraction_parent = secure_directory(cache / "extracted" / key)
        executable_name = "terraform-provider-litellm_v2.0.1"
        executable = extract_executable(archive, extraction_parent / "v2.0.1", executable_name, expected["executable_sha256"])
        # Product/version are selected by the signed exact filename, exact archive
        # member, and protocol manifest; Terraform later executes this digest.
        mirror = secure_directory(cache / "mirror" / "registry.terraform.io" / "ncecere" / "litellm")
        mirror_archive = mirror / name
        if mirror_archive.exists() or mirror_archive.is_symlink():
            if hash_file(mirror_archive) != expected["sha256"]:
                raise HarnessError("provider mirror archive was replaced")
        else:
            temporary = mirror / (".mirror-" + secrets.token_hex(16))
            descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0), 0o600)
            try:
                source = os.open(archive, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
                try:
                    while True:
                        chunk = os.read(source, 1024 * 1024)
                        if not chunk:
                            break
                        os.write(descriptor, chunk)
                    os.fsync(descriptor)
                finally:
                    os.close(source)
            finally:
                os.close(descriptor)
            os.replace(temporary, mirror_archive)
        if hash_file(mirror_archive) != expected["sha256"] or hash_file(executable) != expected["executable_sha256"]:
            raise HarnessError("verified provider cache changed before use")
    print(
        "Verified provider 2.0.1: fingerprint=" + tools["signing_key"]["fingerprint"]
        + " signature_sha256=" + tools["signature_sha256"]
        + " archive_sha256=" + expected["sha256"]
        + " executable_sha256=" + expected["executable_sha256"]
        + " manifest_sha256=" + tools["manifest_sha256"]
    )
    return 0


def prepare_provider(args: argparse.Namespace) -> int:
    parent = secure_directory(Path(args.directory))
    bundle, _ = make_provider_bundle(parent, Path(args.provider_binary))
    print(str(bundle))
    return 0


def report_from_records(args: argparse.Namespace) -> int:
    matrix = load_json(MATRIX_PATH)
    results: list[dict] = []
    require_regular_file(Path(args.records))
    evidence = {
        "resource_coverage": "apply-refresh-plan-destroy",
        "lifecycle": "apply-refresh-plan-destroy",
        "drift": "apply-refresh-plan-destroy",
        "data_source": "apply-refresh-plan",
        "upgrade": "upgrade-refresh-plan",
        "import": "import-refresh-apply-plan-detach",
        "replacement": "replacement-plan-apply",
        "failure_recovery": "fault-retry-converged",
        "optional_feature": "apply-refresh-plan-destroy",
        "documentation": "documentation-validated",
    }
    for number, line in enumerate(Path(args.records).read_text(encoding="utf-8").splitlines(), 1):
        fields = line.split("\t")
        if len(fields) not in (5, 6):
            raise HarnessError("execution record has an invalid field count")
        name, category, status, reason, title = fields[:5]
        evidence_override = fields[5] if len(fields) == 6 else ""
        if ":" not in name or category not in evidence or status not in STATUSES:
            raise HarnessError("execution record has an invalid controlled label")
        subject = name.split(":", 1)[1]
        if name != f"{category}:{subject}":
            raise HarnessError("execution record name/category mismatch")
        if not re.fullmatch(r"[a-z0-9_.-]+", subject):
            raise HarnessError("execution record subject is not controlled")
        if evidence_override and evidence_override not in EVIDENCE_CODES:
            raise HarnessError("execution record evidence code is not controlled")
        item = {"name": name, "subject": subject, "category": category, "status": status, "evidence_code": evidence_override or evidence[category]}
        if reason:
            item["reason"] = reason
            if reason == "enterprise-license-required":
                item["evidence_code"] = "enterprise-unavailable"
            elif reason in {"inventory-endpoint-may-be-unavailable", "api-endpoint-unavailable"}:
                item["evidence_code"] = "api-unavailable"
            elif reason == "cli-version-below-1.11":
                item["evidence_code"] = "cli-feature-unavailable"
            elif reason == "previous-release-resource-unavailable":
                item["evidence_code"] = "previous-release-unavailable"
        if title:
            code = DIAGNOSTIC_TITLE_CODES.get(title)
            if not code:
                raise HarnessError("child diagnostic title is not exactly allowlisted")
            item["diagnostic_code"] = code
        results.append(item)
    account_skips(results, set(matrix["allowed_skip_reasons"]) | {"api-endpoint-unavailable"})
    resources = {entry["type"] for entry in matrix["resources"]}
    data_sources = set(matrix["data_sources"])
    expected = {
        "resource_coverage": resources, "lifecycle": resources, "drift": resources,
        "upgrade": resources, "import": resources,
        "data_source": data_sources,
        "replacement": {entry["name"] for entry in matrix["replacement_scenarios"]},
        "failure_recovery": {entry["name"] for entry in matrix["failure_recovery_scenarios"]},
        "optional_feature": set(matrix["optional_features"]),
        "documentation": set(matrix["documentation_scenarios"]),
    }
    for category, subjects in expected.items():
        actual = {item["subject"] for item in results if item["category"] == category}
        if actual != subjects:
            raise HarnessError("missing or unexplained per-scenario execution result")
    product = "opentofu" if "tofu" in Path(args.cli).name else "terraform"
    cli_version = selected_cli_version(args.cli)
    provider_digest, schema_digest = provider_schema_fingerprint(args.cli, Path(args.provider_binary))
    previous = load_json(TOOLS_PATH)["previous_provider"]
    previous_archive = previous["archives"][platform_key()]
    previous_executable_digest, previous_schema_digest = provider_schema_fingerprint(
        args.cli, Path(args.previous_provider_binary)
    )
    if previous_executable_digest != previous_archive["executable_sha256"] or previous_schema_digest != previous["schema_sha256"]:
        raise HarnessError("executed previous provider digest/schema differs from the exact pin")
    provenance = {
        "cli_product": product, "cli_version": cli_version,
        "cli_executable_sha256": hash_file(Path(args.cli)),
        "provider_executable_sha256": provider_digest,
        "provider_schema_sha256": schema_digest,
        "previous_signature_sha256": previous["signature_sha256"],
        "previous_archive_sha256": previous_archive["sha256"],
        "previous_executable_sha256": previous_executable_digest,
        "previous_provider_schema_sha256": previous_schema_digest,
        "previous_manifest_sha256": previous["manifest_sha256"],
        "previous_signing_fingerprint": previous["signing_key"]["fingerprint"],
    }
    report = {"schema_version": REPORT_SCHEMA_VERSION, "mode": "destructive-local", "summary": summarize_results(results), "scenarios": results, "provenance": provenance}
    write_report(Path(args.report), report)
    print(f"Execution report passed strict validation: emitted={len(results)}")
    return 0


def preflight(args: argparse.Namespace) -> int:
    if args.target == "local":
        if os.environ.get("TF_ACC") != "1" or os.environ.get("LITELLM_ACCEPTANCE_CONFIRM") != "local-v1.98.0":
            raise HarnessError("local destructive matrix requires both documented confirmations")
    else:
        if os.environ.get("TF_ACC") != "1" or os.environ.get("LITELLM_REMOTE_ACCEPTANCE_CONFIRM") != "dev-disposable-objects-only":
            raise HarnessError("remote destructive matrix requires its separate documented confirmation")
        namespace = os.environ.get("LITELLM_TEST_NAMESPACE", "")
        if not re.fullmatch(r"issue210-[a-z0-9]{8,32}", namespace):
            raise HarnessError("remote matrix requires a unique issue210 namespace")
    print(f"{args.target.capitalize()} destructive preflight passed; no mutation has been performed")
    return 0


def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser()
    commands = value.add_subparsers(dest="command", required=True)
    assemble = commands.add_parser("assembly")
    assemble.add_argument("--cli", default="terraform")
    assemble.add_argument("--provider-binary")
    assemble.add_argument("--report")
    assemble.set_defaults(function=assembly)
    tool = commands.add_parser("install-tool")
    tool.add_argument("product", choices=("terraform", "opentofu"))
    tool.add_argument("version")
    tool.add_argument("--cache", default="~/.cache/terraform-provider-litellm/tools")
    tool.add_argument("--offline", action="store_true")
    tool.set_defaults(function=install_tool)
    previous = commands.add_parser("install-previous")
    previous.add_argument("--cache", default="~/.cache/terraform-provider-litellm/providers")
    previous.add_argument("--offline", action="store_true")
    previous.set_defaults(function=install_previous)
    bundle = commands.add_parser("prepare-provider")
    bundle.add_argument("--provider-binary", required=True)
    bundle.add_argument("--directory", required=True)
    bundle.set_defaults(function=prepare_provider)
    reports = commands.add_parser("report-records")
    reports.add_argument("--records", required=True)
    reports.add_argument("--report", required=True)
    reports.add_argument("--cli", required=True)
    reports.add_argument("--provider-binary", required=True)
    reports.add_argument("--previous-provider-binary", required=True)
    reports.set_defaults(function=report_from_records)
    safety = commands.add_parser("preflight")
    safety.add_argument("target", choices=("local", "remote"))
    safety.set_defaults(function=preflight)
    return value


def main() -> int:
    args = parser().parse_args()
    try:
        return args.function(args)
    except (HarnessError, OSError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
        print(f"Matrix failed: {redact(str(error))}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
