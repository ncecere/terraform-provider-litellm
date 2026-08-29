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
import importlib.util
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
REPORT_SCHEMA_VERSION = 3
EVIDENCE_SCHEMA_VERSION = 1
MAX_EVIDENCE_LEDGER = 8 * 1024 * 1024
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
    "provider_executable_sha256", "provider_schema_sha256", "candidate_commit",
    "harness_sha256", "matrix_sha256", "evidence_ledger_sha256", "run_nonce_sha256",
    "previous_signature_sha256", "previous_archive_sha256",
    "previous_executable_sha256", "previous_provider_schema_sha256",
    "previous_manifest_sha256", "previous_signing_fingerprint",
}
EVIDENCE_CODES = {
    "inventory-validated", "format-validated", "provider-schema-validated",
    "apply-refresh-plan-destroy", "apply-refresh-plan", "import-refresh-apply-plan-detach",
    "upgrade-refresh-plan", "upgrade-reviewed-migration",
    "upgrade-reviewed-private-migration", "replacement-plan-apply", "fault-retry-converged",
    "api-unavailable", "enterprise-unavailable", "cli-feature-unavailable",
    "previous-release-unavailable", "documentation-validated", "remote-mutation-disabled",
}
DIAGNOSTIC_TITLE_CODES = {
    "model_failed_create_retry": ("Client Error", "model-create-error"),
    "team_failed_create_retry": ("Team Creation Outcome Uncertain", "team-create-outcome-uncertain"),
    "agent_role_redacted_import": ("Unsupported Agent Clear", "agent-role-redacted-read"),
    "agent_import_public_projection": ("Provider produced invalid plan", "agent-import-public-projection-unavailable"),
    "fallback_delete_not_authoritative": ("Fallback Delete Unconfirmed", "fallback-delete-not-authoritative"),
    "key_wo_endpoint_unavailable": ("Write-Only Key Creation Error", "key-write-only-endpoint-unavailable"),
}
DIAGNOSTIC_CODES = {value[1] for value in DIAGNOSTIC_TITLE_CODES.values()}
ASSERTION_CODES = {
    "terraform-plan-state-api", "upgrade-state-migration",
    "upgrade-private-plan-trigger-migration", "import-authoritative-absence",
    "import-fail-closed-inconclusive-absence", "import-immediate-no-drift-provenance",
    "replacement-plan-state",
    "fault-endpoint-diagnostic-state", "bounded-feature-attempt",
    "validated-documentation", "allowlisted-unavailability",
    "refresh-only-config-state-zero-drift",
}
MODERN_MANDATORY_SKIPS = {
    ("resource_coverage", "litellm_project", "enterprise-license-required"),
    ("upgrade", "litellm_jwt_key_mapping", "previous-release-resource-unavailable"),
    ("upgrade", "litellm_project", "enterprise-license-required"),
    ("lifecycle", "litellm_project", "enterprise-license-required"),
    ("import", "litellm_agent", "role-redacted-state-requires-admin"),
    ("import", "litellm_project", "enterprise-license-required"),
    ("drift", "litellm_project", "enterprise-license-required"),
    ("data_source", "litellm_project", "enterprise-license-required"),
    ("data_source", "litellm_projects", "enterprise-license-required"),
    ("optional_feature", "key_wo", "api-endpoint-unavailable"),
}
FALLBACK_CONDITIONAL_SKIPS = {
    ("lifecycle", "litellm_fallback", "fallback-delete-not-authoritative"),
    ("import", "litellm_fallback", "fallback-delete-not-authoritative"),
}
PRE_111_MANDATORY_SKIPS = (MODERN_MANDATORY_SKIPS - {
    ("import", "litellm_agent", "role-redacted-state-requires-admin"),
    ("optional_feature", "key_wo", "api-endpoint-unavailable"),
}) | {
    ("resource_coverage", "litellm_jwt_key_mapping", "cli-version-below-1.11"),
    ("lifecycle", "litellm_jwt_key_mapping", "cli-version-below-1.11"),
    ("drift", "litellm_jwt_key_mapping", "cli-version-below-1.11"),
    ("data_source", "litellm_jwt_key_mapping", "cli-version-below-1.11"),
    ("data_source", "litellm_jwt_key_mappings", "cli-version-below-1.11"),
    ("import", "litellm_jwt_key_mapping", "cli-version-below-1.11"),
    ("replacement", "jwt_claim_pair_identity", "cli-version-below-1.11"),
    ("optional_feature", "send_invite_email", "cli-version-below-1.11"),
    ("optional_feature", "key_wo", "cli-version-below-1.11"),
    ("optional_feature", "jwt_key_mapping_key_wo", "cli-version-below-1.11"),
}
PRE_111_AGENT_SKIPS = {
    ("import", "litellm_agent", "role-redacted-state-requires-admin"),
    ("import", "litellm_agent", "cli-version-below-1.11-agent-import-projection"),
}
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
    lifecycle_skips = {
        (item["type"], item.get("lifecycle_skip_reason"), item.get("lifecycle_skip_diagnostic_code"))
        for item in resources if item.get("lifecycle_skip_reason") or item.get("lifecycle_skip_diagnostic_code")
    }
    if lifecycle_skips != {(
        "litellm_fallback", "fallback-delete-not-authoritative",
        "fallback-delete-not-authoritative",
    )}:
        raise HarnessError("fallback lifecycle skip contract changed without review")
    expected_counts = {
        "resource_coverage": 24, "upgrade": 24, "lifecycle": 24, "import": 24,
        "drift": 24, "replacement": 3, "failure_recovery": 2,
        "data_source": 35, "documentation": 3,
    }
    if matrix.get("scenario_counts") != expected_counts:
        raise HarnessError("scenario count contract changed without explicit accounting")
    if sum(expected_counts.values()) + len(matrix.get("optional_features", [])) != 166:
        raise HarnessError("execution matrix must contain exactly 166 independently reviewed scenarios")
    expected_skips = matrix.get("terraform_1_11_4_expected_skips", [])
    skip_identities = {(item.get("category"), item.get("subject"), item.get("reason")) for item in expected_skips}
    conditional_skips = matrix.get("terraform_1_11_4_conditional_skips", [])
    conditional_identities = {(item.get("category"), item.get("subject"), item.get("reason")) for item in conditional_skips}
    if len(expected_skips) != 10 or skip_identities != MODERN_MANDATORY_SKIPS:
        raise HarnessError("modern CLI lanes must have the exact ten independently reviewed mandatory skips")
    if len(conditional_skips) != 2 or conditional_identities != FALLBACK_CONDITIONAL_SKIPS:
        raise HarnessError("fallback conditional skip inventory is not exact")
    for scenario in matrix.get("replacement_scenarios", []):
        if scenario.get("name") == "jwt_claim_pair_identity" and scenario.get("minimum_cli") != "1.11.0":
            raise HarnessError("JWT replacement does not have an exact CLI feature gate")
        if (
            scenario.get("compare") != "hmac-sha256"
            or scenario.get("expected_actions") != ["create", "delete"]
            or scenario.get("dependency_check") is not True
            or not scenario.get("dependency_address")
            or not scenario.get("post_replacement_no_drift")
        ):
            raise HarnessError("replacement evidence contract is incomplete")
        if not (HERE / scenario["fixture"]).is_file():
            raise HarnessError("replacement fixture is missing")
    current_only = matrix.get("current_provider_only_scenarios", [])
    if current_only != [{
        "name": "mcp-immediate-import-no-drift-provenance", "fixture": "mcp_server_import.tf",
        "resource_type": "litellm_mcp_server", "scenario": "import:litellm_mcp_server",
        "prior_release_expected": False,
        "assertions": ["immediate-import", "two-refresh-only-no-drift", "provenance-preserved"],
    }] or not (ROOT / "internal_testing" / "resources" / current_only[0]["fixture"]).is_file():
        raise HarnessError("current-only MCP import provenance inventory is incomplete")
    for scenario in matrix.get("failure_recovery_scenarios", []):
        expected = DIAGNOSTIC_TITLE_CODES.get(scenario.get("name", ""))
        if (
            not scenario.get("cleanup_required") or not scenario.get("expected_diagnostic_title")
            or not expected
            or (scenario.get("expected_diagnostic_title"), scenario.get("expected_diagnostic_code")) != expected
        ):
            raise HarnessError("failure-recovery accounting is incomplete")
        if not (HERE / scenario["fixture"]).is_file():
            raise HarnessError("failure-recovery fixture is missing")
    computed = matrix.get("upgrade_expected_computed_migrations", {})
    nested_masks = sorted(
        (resource_type, path)
        for resource_type, paths in computed.items()
        for path in paths if "." in path or "[*]" in path
    )
    if nested_masks != [("litellm_team_member_add", "member[*].user_id")]:
        raise HarnessError("the reviewed nested computed migration inventory changed")
    if matrix.get("upgrade_expected_representation_migrations") != {
        "litellm_agent": {
            "agent_card.signatures": "missing-to-empty-list-block",
            "agent_card.supports_authenticated_extended_card": "missing-to-null-bool",
        }
    }:
        raise HarnessError("the reviewed representation migration inventory changed")
    private_migrations = matrix.get("upgrade_expected_private_migrations", [])
    if (
        matrix.get("upgrade_expected_private_plan_triggers")
        != {"litellm_agent": ["id"]}
        or "litellm_agent" not in private_migrations
    ):
        raise HarnessError("reviewed private plan trigger inventory is incomplete")
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
    schema_by_cli = previous.get("schema_sha256_by_cli", {})
    expected_schema_lanes = {
        f"{product}-{version}"
        for product in ("terraform", "opentofu")
        for version in tools[product]
    }
    if set(schema_by_cli) != expected_schema_lanes:
        raise HarnessError("previous provider schema pins do not cover the exact CLI matrix")
    digests = [
        *previous.get("registry_metadata_sha256", {}).values(), previous.get("checksums_file_sha256"),
        previous.get("signature_sha256"), previous.get("manifest_sha256"),
        *schema_by_cli.values(),
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
        f'    "registry.opentofu.org/ncecere/litellm" = {json.dumps(str(provider_dir))}\n'
        "  }\n}\n"
    ).encode()
    try:
        os.write(descriptor, encoded)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    return config


def validate_upgrade_schema_contract(provider_schema: dict) -> None:
    spec = importlib.util.spec_from_file_location(
        "litellm_upgrade_state_contract", HERE / "upgrade_state.py"
    )
    if spec is None or spec.loader is None:
        raise HarnessError("upgrade contract validator could not be loaded")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    try:
        module.compile_upgrade_contract(provider_schema, load_json(MATRIX_PATH))
    except module.UpgradeStateError as error:
        raise HarnessError("upgrade migration contract failed current-schema validation") from error


def provider_schema_fingerprint(
    cli: str, provider_binary: Path, *, validate_upgrade_contract: bool = True,
) -> tuple[str, str]:
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
        if validate_upgrade_contract:
            validate_upgrade_schema_contract(selected)
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
    # Older Terraform releases accept one directory per fmt invocation.
    for directory in ("examples", "internal_testing"):
        code, _ = safe_run([cli, "fmt", "-check", "-recursive", directory], ROOT)
        if code != 0:
            raise HarnessError("published or internal HCL is not formatted")


def credential_scan(paths: Iterable[Path]) -> None:
    for path in paths:
        require_regular_file(path)
        text = path.read_text(encoding="utf-8", errors="replace")
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
        # Receipt HMACs are strict, value-free digests. Their required
        # `hmac-sha256:<hex>` spelling can resemble a host:port to the broad URL
        # detector when the digest starts with decimal digits. Remove only the
        # already shape-validated exact token before scanning all other bytes.
        scanned = re.sub(r"hmac-sha256:[0-9a-f]{64}", "[SAFE_HMAC]", text)
        if any(pattern.search(scanned) for pattern in (URL_RE, UUID_RE, ABS_PATH_RE, SECRET_RE, ID_RE)):
            raise HarnessError("result artifact failed secret/identity/location scan")


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
        elif key == "candidate_commit":
            if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
                raise HarnessError("report candidate commit is invalid")
        elif key == "previous_signing_fingerprint":
            if value != "C753834A70062246C92CEF56F0A1AEC231353F8B":
                raise HarnessError("report signing fingerprint is invalid")
        elif not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{64}", value):
            raise HarnessError("report digest is invalid")


@contextlib.contextmanager
def secure_parent_fd(path: Path):
    """Open/create every ancestor with openat+O_NOFOLLOW and retain the FD.

    Holding the final directory descriptor makes ancestor and mid-operation
    rename/symlink swaps irrelevant to the eventual exclusive link.
    """
    absolute = path.expanduser().absolute()
    if absolute.name in {"", ".", ".."} or ".." in absolute.parts:
        raise HarnessError("report path is not a safe file path")
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(absolute.anchor, flags)
    try:
        for component in absolute.parent.parts[1:]:
            if component in {"", ".", ".."}:
                raise HarnessError("report path contains an unsafe component")
            try:
                child = os.open(component, flags, dir_fd=descriptor)
            except FileNotFoundError:
                os.mkdir(component, 0o700, dir_fd=descriptor)
                child = os.open(component, flags, dir_fd=descriptor)
            info = os.fstat(child)
            if not stat.S_ISDIR(info.st_mode):
                os.close(child)
                raise HarnessError("report ancestor is not a real directory")
            os.close(descriptor)
            descriptor = child
        info = os.fstat(descriptor)
        if stat.S_IMODE(info.st_mode) & 0o077:
            raise HarnessError("report directory permissions are too broad")
        yield descriptor, absolute.name
    except OSError as error:
        if error.errno in {errno.ELOOP, errno.ENOTDIR}:
            raise HarnessError("report path contains a symlink ancestor") from error
        raise
    finally:
        os.close(descriptor)


def atomic_exclusive_write(path: Path, encoded: bytes) -> None:
    with secure_parent_fd(path) as (directory_fd, final_name):
        temporary_name = ".report-" + secrets.token_hex(16) + ".tmp"
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(temporary_name, flags, 0o600, dir_fd=directory_fd)
        try:
            os.write(descriptor, encoded)
            os.fsync(descriptor)
            info = os.fstat(descriptor)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
                raise HarnessError("temporary report is not an exclusive regular file")
        finally:
            os.close(descriptor)
        try:
            os.link(
                temporary_name, final_name, src_dir_fd=directory_fd,
                dst_dir_fd=directory_fd, follow_symlinks=False,
            )
            final_fd = os.open(final_name, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0), dir_fd=directory_fd)
            try:
                published = bytearray()
                while len(published) <= len(encoded):
                    chunk = os.read(final_fd, min(1024 * 1024, len(encoded) + 1 - len(published)))
                    if not chunk:
                        break
                    published.extend(chunk)
                if bytes(published) != encoded:
                    raise HarnessError("report changed during atomic publication")
            finally:
                os.close(final_fd)
            os.fsync(directory_fd)
        except FileExistsError as error:
            raise HarnessError("report destination already exists") from error
        finally:
            os.unlink(temporary_name, dir_fd=directory_fd)


def scanned_report_bytes(report: dict) -> bytes:
    validate_report(report)
    encoded = (json.dumps(report, indent=2, sort_keys=True) + "\n").encode()
    # Scan the exact bytes before publication; no path reopen is needed and a
    # renamed ancestor cannot redirect the directory-FD operation.
    with tempfile.TemporaryDirectory(prefix="litellm-report-scan-") as raw:
        scan = Path(raw) / "value.json"
        scan.write_bytes(encoded)
        credential_scan([scan])
    return encoded


def write_report(path: Path, report: dict) -> None:
    atomic_exclusive_write(path, scanned_report_bytes(report))


def publish_execution_artifacts(report_path: Path, evidence_path: Path, report: dict, ledger_encoded: bytes) -> None:
    report_encoded = scanned_report_bytes(report)
    with tempfile.TemporaryDirectory(prefix="litellm-ledger-scan-") as raw:
        scan = Path(raw) / "ledger.json"
        scan.write_bytes(b"[" + b",".join(ledger_encoded.splitlines()) + b"]")
        credential_scan([scan])
    # The report is the commit marker. An evidence-only artifact is incomplete
    # and cannot pass release validation; a report is never visible without its
    # already-published, digest-bound evidence ledger.
    atomic_exclusive_write(evidence_path, ledger_encoded)
    atomic_exclusive_write(report_path, report_encoded)


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


def canonicalize_trusted_os_alias(path: Path) -> Path:
    """Canonicalize only immutable root-owned top-level OS aliases.

    macOS exposes trusted paths such as /tmp and /var as root-owned aliases into
    /private.  Resolving an arbitrary path would also trust attacker-created
    ancestor symlinks, so only the first component may be rewritten and only
    when both it and the filesystem root are owned by uid 0.
    """
    value = path.expanduser().absolute()
    if len(value.parts) < 2:
        return value
    first = Path(value.anchor) / value.parts[1]
    try:
        first_info = first.lstat()
        root_info = Path(value.anchor).stat()
    except FileNotFoundError:
        return value
    if stat.S_ISLNK(first_info.st_mode):
        if first_info.st_uid != 0 or root_info.st_uid != 0:
            raise HarnessError("path contains an untrusted ancestor symlink")
        resolved = first.resolve(strict=True)
        resolved_info = resolved.stat()
        if not stat.S_ISDIR(resolved_info.st_mode) or resolved_info.st_uid != 0:
            raise HarnessError("trusted OS alias does not resolve to a root-owned directory")
        value = resolved.joinpath(*value.parts[2:])
    return value


@contextlib.contextmanager
def secure_directory_fd(path: Path, *, create: bool):
    """Retain a no-follow descriptor while walking/creating a directory."""
    value = canonicalize_trusted_os_alias(path)
    flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    descriptor = os.open(value.anchor, flags)
    try:
        for component in value.parts[1:]:
            try:
                child = os.open(component, flags, dir_fd=descriptor)
            except FileNotFoundError:
                if not create:
                    raise HarnessError("required directory is missing")
                try:
                    os.mkdir(component, 0o700, dir_fd=descriptor)
                except FileExistsError:
                    # A concurrent trusted installer may have won the mkdir;
                    # the no-follow open below still rejects a symlink swap.
                    pass
                child = os.open(component, flags, dir_fd=descriptor)
            info = os.fstat(child)
            if not stat.S_ISDIR(info.st_mode):
                os.close(child)
                raise HarnessError("path contains a non-directory component")
            os.close(descriptor)
            descriptor = child
        yield value, descriptor
    except OSError as error:
        if error.errno in {errno.ELOOP, errno.ENOTDIR}:
            raise HarnessError("path contains an untrusted ancestor symlink") from error
        raise
    finally:
        os.close(descriptor)


def secure_directory(path: Path) -> Path:
    with secure_directory_fd(path, create=True) as (value, descriptor):
        os.fchmod(descriptor, 0o700)
        info = os.fstat(descriptor)
        if stat.S_IMODE(info.st_mode) & 0o077:
            raise HarnessError("private directory permissions are too broad")
    return value


def make_private_temp(args: argparse.Namespace) -> int:
    with secure_directory_fd(Path(args.base), create=False) as (base, descriptor):
        for _ in range(32):
            name = "litellm-issue210." + secrets.token_hex(8)
            try:
                os.mkdir(name, 0o700, dir_fd=descriptor)
                os.fsync(descriptor)
                print(str(base / name))
                return 0
            except FileExistsError:
                continue
    raise HarnessError("could not allocate a unique private scratch directory")


def canonical_path(args: argparse.Namespace) -> int:
    path = canonicalize_trusted_os_alias(Path(args.path))
    current = Path(path.anchor)
    for part in path.parts[1:]:
        current = current / part
        if current.exists() or current.is_symlink():
            info = current.lstat()
            if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
                raise HarnessError("path contains an untrusted ancestor symlink")
    print(str(path))
    return 0


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
        mirror_archives = []
        for registry_host in ("registry.terraform.io", "registry.opentofu.org"):
            mirror = secure_directory(cache / "mirror" / registry_host / "ncecere" / "litellm")
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
            mirror_archives.append(mirror_archive)
        if any(hash_file(value) != expected["sha256"] for value in mirror_archives) or hash_file(executable) != expected["executable_sha256"]:
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


def _git_commit() -> str:
    proc = subprocess.run(
        ["git", "rev-parse", "HEAD"], cwd=ROOT, stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=30, check=False,
    )
    value = proc.stdout.strip()
    if proc.returncode or not re.fullmatch(r"[0-9a-f]{40}", value):
        raise HarnessError("candidate commit could not be bound")
    return value


def _read_session(path: Path) -> dict:
    require_regular_file(path)
    value = load_json(path)
    required = {
        "schema_version", "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256", "ledger", "key_file",
    }
    if set(value) != required or value["schema_version"] != EVIDENCE_SCHEMA_VERSION:
        raise HarnessError("evidence session schema is invalid")
    digest_fields = ("run_nonce", "provider_sha256", "provider_schema_sha256", "harness_sha256", "matrix_sha256")
    if any(not re.fullmatch(r"[0-9a-f]{64}", str(value[field])) for field in digest_fields):
        raise HarnessError("evidence session digest is invalid")
    if not re.fullmatch(r"[0-9a-f]{40}", str(value["candidate_commit"])):
        raise HarnessError("evidence session candidate is invalid")
    if not re.fullmatch(r"(?:terraform|opentofu)-\d+\.\d+\.\d+", str(value["cli_lane"])):
        raise HarnessError("evidence session CLI lane is invalid")
    key_path = Path(value["key_file"])
    key_info = require_regular_file(key_path)
    if stat.S_IMODE(key_info.st_mode) & 0o077 or key_info.st_size != 32:
        raise HarnessError("evidence session signing key is unsafe")
    return value


def _session_key(session: dict) -> bytes:
    path = Path(session["key_file"])
    require_regular_file(path)
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    try:
        value = os.read(descriptor, 33)
    finally:
        os.close(descriptor)
    if len(value) != 32:
        raise HarnessError("evidence signing key has an invalid size")
    return value


def _sign_record(item: dict, key: bytes) -> str:
    canonical = json.dumps(item, sort_keys=True, separators=(",", ":")).encode()
    return safe_hmac(canonical, key)


def _append_ledger(path: Path, item: dict) -> None:
    encoded = (json.dumps(item, sort_keys=True, separators=(",", ":")) + "\n").encode()
    descriptor = os.open(path, os.O_WRONLY | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0))
    try:
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size + len(encoded) > MAX_EVIDENCE_LEDGER:
            raise HarnessError("evidence ledger is unsafe or exceeded its bound")
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        os.write(descriptor, encoded)
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def start_session(args: argparse.Namespace) -> int:
    cli = Path(args.cli)
    provider = Path(args.provider_binary)
    provider_digest, schema_digest = provider_schema_fingerprint(str(cli), provider)
    version = selected_cli_version(str(cli))
    product = "opentofu" if "tofu" in cli.name else "terraform"
    nonce_digest = hashlib.sha256(("issue210-run-v1\0" + secrets.token_hex(32)).encode()).hexdigest()
    key_path = Path(args.session).absolute().parent / (".ledger-key-" + secrets.token_hex(16))
    atomic_exclusive_write(key_path, secrets.token_bytes(32))
    session = {
        "schema_version": EVIDENCE_SCHEMA_VERSION,
        "run_nonce": nonce_digest,
        "cli_lane": f"{product}-{version}",
        "candidate_commit": _git_commit(),
        "provider_sha256": provider_digest,
        "provider_schema_sha256": schema_digest,
        "harness_sha256": hash_file(Path(__file__).resolve()),
        "matrix_sha256": hash_file(MATRIX_PATH),
        "ledger": str(Path(args.ledger).absolute()), "key_file": str(key_path),
    }
    header = {"record_type": "session", **{key: value for key, value in session.items() if key not in {"ledger", "key_file"}}}
    header["receipt_hmac"] = _sign_record(header, _session_key(session))
    atomic_exclusive_write(Path(args.ledger), (json.dumps(header, sort_keys=True, separators=(",", ":")) + "\n").encode())
    atomic_exclusive_write(Path(args.session), (json.dumps(session, sort_keys=True) + "\n").encode())
    print(json.dumps(session, sort_keys=True))
    return 0


def _ledger_values(session: dict) -> list[dict]:
    ledger = Path(session["ledger"])
    require_regular_file(ledger)
    if ledger.stat().st_size > MAX_EVIDENCE_LEDGER:
        raise HarnessError("evidence ledger exceeded its bound")
    values = []
    for line in ledger.read_text(encoding="utf-8").splitlines():
        try:
            item = json.loads(line)
        except json.JSONDecodeError as error:
            raise HarnessError("evidence ledger contains invalid JSON") from error
        if not isinstance(item, dict):
            raise HarnessError("evidence ledger record is not an object")
        values.append(item)
    header_keys = {
        "record_type", "schema_version", "run_nonce", "cli_lane", "candidate_commit",
        "provider_sha256", "provider_schema_sha256", "harness_sha256", "matrix_sha256", "receipt_hmac",
    }
    if not values or values[0].get("record_type") != "session" or set(values[0]) != header_keys or values[0].get("schema_version") != EVIDENCE_SCHEMA_VERSION:
        raise HarnessError("evidence ledger lacks its strict session header")
    bindings = {key: session[key] for key in (
        "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256",
    )}
    if any(values[0].get(key) != value for key, value in bindings.items()):
        raise HarnessError("evidence ledger session binding changed")
    signing_key = _session_key(session)
    for item in values:
        signature = item.get("receipt_hmac")
        unsigned = {key: value for key, value in item.items() if key != "receipt_hmac"}
        if not isinstance(signature, str) or not hmac.compare_digest(signature, _sign_record(unsigned, signing_key)):
            raise HarnessError("evidence ledger receipt signature is invalid")
    return values


def _expected_subjects(matrix: dict) -> dict[str, set[str]]:
    resources = {entry["type"] for entry in matrix["resources"]}
    return {
        "resource_coverage": resources, "lifecycle": resources, "drift": resources,
        "upgrade": resources, "import": resources,
        "data_source": set(matrix["data_sources"]),
        "replacement": {entry["name"] for entry in matrix["replacement_scenarios"]},
        "failure_recovery": {entry["name"] for entry in matrix["failure_recovery_scenarios"]},
        "optional_feature": set(matrix["optional_features"]),
        "documentation": set(matrix["documentation_scenarios"]),
    }


def _command_digest(command: list[str]) -> str:
    encoded = json.dumps(command, separators=(",", ":"), ensure_ascii=False).encode()
    return hashlib.sha256(b"issue210-command-v1\0" + encoded).hexdigest()


def _assertion_digest(paths: list[Path], assertion: str) -> str:
    if not paths:
        raise HarnessError("scenario record requires observed assertion evidence")
    digest = hashlib.sha256(b"issue210-assertion-v1\0" + assertion.encode() + b"\0")
    total = 0
    for path in paths:
        total += require_regular_file(path).st_size
        if total > 10 * 1024 * 1024:
            raise HarnessError("scenario assertion evidence exceeded its bound")
        digest.update(bytes.fromhex(hash_file(path)))
    return digest.hexdigest()


def _command_for_output(commands: list[dict], evidence: Path, *, require_failed: bool) -> dict:
    require_regular_file(evidence)
    raw = evidence.read_bytes()
    if len(raw) > 2 * 1024 * 1024:
        raise HarnessError("scenario command evidence exceeds its private bound")
    matches = []
    for item in commands:
        exit_code = item.get("exit_code")
        if not isinstance(exit_code, int) or ((exit_code != 0) != require_failed):
            continue
        encoded = exit_code.to_bytes(4, "big", signed=True) + raw
        digest = hashlib.sha256(b"issue210-result-v1\0" + encoded).hexdigest()
        if hmac.compare_digest(digest, str(item.get("result_sha256", ""))):
            matches.append(item)
    triples = {(item["command_sha256"], item["result_sha256"], item["exit_code"]) for item in matches}
    if len(triples) != 1:
        kind = "failed" if require_failed else "successful"
        raise HarnessError(f"scenario command evidence is not bound to one exact {kind} command result")
    return matches[-1]


def _command_for_diagnostic(commands: list[dict], evidence: Path) -> dict:
    return _command_for_output(commands, evidence, require_failed=True)


def _append_scenario(session: dict, *, name: str, category: str, status: str,
                     reason: str, diagnostic_code: str, assertion: str,
                     evidence_paths: list[Path], command_record: dict | None = None,
                     diagnostic_evidence: Path | None = None) -> None:
    matrix = load_json(MATRIX_PATH)
    if category not in _expected_subjects(matrix) or ":" not in name:
        raise HarnessError("scenario observation category/name is invalid")
    subject = name.split(":", 1)[1]
    if name != f"{category}:{subject}" or subject not in _expected_subjects(matrix)[category]:
        raise HarnessError("scenario observation is not in the checked matrix")
    if status not in STATUSES or assertion not in ASSERTION_CODES:
        raise HarnessError("scenario observation enum is invalid")
    if status == "skipped":
        if reason not in set(matrix["allowed_skip_reasons"]) | {"api-endpoint-unavailable"}:
            raise HarnessError("scenario observation skip is not allowlisted")
    elif reason:
        raise HarnessError("non-skipped scenario observation has a reason")
    if diagnostic_code and diagnostic_code not in DIAGNOSTIC_CODES:
        raise HarnessError("scenario observation diagnostic is not allowlisted")
    if bool(diagnostic_code) != bool(diagnostic_evidence):
        raise HarnessError("scenario diagnostic code and exact command evidence must be paired")
    values = _ledger_values(session)
    commands = [item for item in values if item.get("record_type") == "command"]
    if not commands:
        raise HarnessError("scenario observation has no executed bounded command")
    if diagnostic_evidence is not None:
        command = _command_for_diagnostic(commands, diagnostic_evidence)
        if diagnostic_evidence not in evidence_paths:
            evidence_paths = [*evidence_paths, diagnostic_evidence]
    else:
        command = command_record or commands[-1]
    bindings = {key: session[key] for key in (
        "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256",
    )}
    if any(command.get(key) != value for key, value in bindings.items()):
        raise HarnessError("scenario command receipt is from another session")
    command_exit_code = command.get("exit_code", command.get("command_exit_code"))
    if not isinstance(command_exit_code, int) or not re.fullmatch(r"[0-9a-f]{64}", str(command.get("command_sha256", ""))) or not re.fullmatch(r"[0-9a-f]{64}", str(command.get("result_sha256", ""))):
        raise HarnessError("scenario command receipt schema is invalid")
    existing = {(item.get("category"), item.get("subject")) for item in values if item.get("record_type") == "scenario"}
    if (category, subject) in existing:
        return
    item = {
        "record_type": "scenario", **bindings, "name": name, "subject": subject,
        "category": category, "status": status, "assertion": assertion,
        "command_sha256": command["command_sha256"], "result_sha256": command["result_sha256"],
        "command_exit_code": command_exit_code,
        "assertion_sha256": _assertion_digest(evidence_paths, assertion),
    }
    if reason:
        item["reason"] = reason
    if diagnostic_code:
        item["diagnostic_code"] = diagnostic_code
    item["receipt_hmac"] = _sign_record(item, _session_key(session))
    _append_ledger(Path(session["ledger"]), item)


def record_observation(args: argparse.Namespace) -> int:
    session = _read_session(Path(args.session))
    evidence_paths = [Path(value) for value in args.evidence]
    presence_paths = [Path(value) for value in (args.fallback_presence_evidence or [])]
    requires_presence = args.name == "import:litellm_fallback" and args.diagnostic_code == "fallback-delete-not-authoritative"
    if bool(presence_paths) != requires_presence or (presence_paths and len(presence_paths) != 3):
        raise HarnessError("fallback import diagnostic and exact presence phase evidence must be paired")
    if presence_paths:
        phase_digest = _assertion_digest([*presence_paths, Path(args.diagnostic_evidence)], "terraform-plan-state-api")
        phases = [
            item for item in _ledger_values(session)
            if item.get("record_type") == "phase"
            and item.get("phase") == "fallback-authoritative-presence"
            and item.get("assertion_sha256") == phase_digest
        ]
        if len(phases) != 1 or phases[0].get("subjects") != ["litellm_fallback"]:
            raise HarnessError("fallback import skip lacks one exact authoritative presence phase")
        evidence_paths.extend(presence_paths)
    _append_scenario(
        session, name=args.name, category=args.category, status=args.status,
        reason=args.reason or "", diagnostic_code=args.diagnostic_code or "",
        assertion=args.assertion, evidence_paths=evidence_paths,
        diagnostic_evidence=Path(args.diagnostic_evidence) if args.diagnostic_evidence else None,
    )
    return 0


def _walk_resources(module: dict) -> Iterable[dict]:
    yield from module.get("resources", [])
    for child in module.get("child_modules", []):
        yield from _walk_resources(child)


def _configured_data_catalog(plan: dict) -> set[tuple[str, str]]:
    root = plan.get("configuration", {}).get("root_module", {})
    rows: set[tuple[str, str]] = set()
    resources = root.get("resources", [])
    if not isinstance(resources, list):
        raise HarnessError("smoke configuration catalog is invalid")
    for item in resources:
        if item.get("mode") != "data":
            continue
        address, resource_type = item.get("address"), item.get("type")
        if not isinstance(address, str) or not isinstance(resource_type, str):
            raise HarnessError("configured data-source address/type is invalid")
        if not re.fullmatch(r"data\.litellm_[a-z_]+\.[A-Za-z0-9_-]+", address):
            raise HarnessError("configured data-source address is outside the root catalog")
        if (address, resource_type) in rows:
            raise HarnessError("configured data-source catalog contains a duplicate")
        rows.add((address, resource_type))
    return rows


def _raw_state_data_catalog(state: dict) -> set[tuple[str, str]]:
    rows: set[tuple[str, str]] = set()
    for item in state.get("resources", []):
        if item.get("mode") != "data":
            continue
        resource_type, name = item.get("type"), item.get("name")
        module = item.get("module", "")
        if not isinstance(resource_type, str) or not isinstance(name, str) or not isinstance(module, str):
            raise HarnessError("refreshed data-source state address/type is invalid")
        address = ".".join(part for part in (module, "data", resource_type, name) if part)
        if not re.fullmatch(r"data\.litellm_[a-z_]+\.[A-Za-z0-9_-]+", address):
            raise HarnessError("refreshed data-source state is outside the root catalog")
        if (address, resource_type) in rows:
            raise HarnessError("refreshed data-source state contains a duplicate")
        rows.add((address, resource_type))
    return rows


def capture_refresh_phase(args: argparse.Namespace) -> int:
    session = _read_session(Path(args.session))
    paths = [Path(args.plan), Path(args.refresh_state)]
    for path in paths:
        require_regular_file(path)
    configured = _configured_data_catalog(load_json(paths[0]))
    refreshed = _raw_state_data_catalog(load_json(paths[1]))
    if configured != refreshed:
        raise HarnessError("refresh-only state does not exactly match the configured data-source catalog")
    if not configured:
        return 0
    expected = _expected_subjects(load_json(MATRIX_PATH))["data_source"]
    subjects = sorted({resource_type for _, resource_type in configured})
    # Multiple configured addresses may intentionally exercise distinct lookup
    # forms of one registered type. Their address/type rows must be exact, while
    # the final per-type scenario subject remains unique.
    if set(subjects) - expected:
        raise HarnessError("refresh-only data-source catalog contains an extra subject")
    values = _ledger_values(session)
    commands = [item for item in values if item.get("record_type") == "command"]
    if not commands or commands[-1].get("exit_code") != 0:
        raise HarnessError("refresh-only phase lacks its immediately preceding successful command")
    command = commands[-1]
    expected_command = [args.cli, "apply", "-refresh-only", "-auto-approve", *args.refresh_argument]
    if command.get("command_sha256") != _command_digest(expected_command):
        raise HarnessError("refresh-only phase command does not match the exact supervised argv")
    bindings = {key: session[key] for key in (
        "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256",
    )}
    item = {
        "record_type": "phase", **bindings, "phase": "refresh-only-data-sources",
        "subjects": subjects, "command_sha256": command["command_sha256"],
        "result_sha256": command["result_sha256"], "command_exit_code": command["exit_code"],
        "assertion_sha256": _assertion_digest(paths, "refresh-only-config-state-zero-drift"),
    }
    item["receipt_hmac"] = _sign_record(item, _session_key(session))
    _append_ledger(Path(session["ledger"]), item)
    return 0


def _require_consecutive_fallback_commands(commands: list[dict], delete_command: dict,
                                           refresh_command: dict, state_command: dict,
                                           expected_delete: str, expected_refresh: str,
                                           expected_state: str) -> None:
    if (
        delete_command.get("command_sha256") != expected_delete
        or refresh_command.get("command_sha256") != expected_refresh
        or state_command.get("command_sha256") != expected_state
        or commands[-3:] != [delete_command, refresh_command, state_command]
    ):
        raise HarnessError("fallback presence proof is not the exact consecutive delete-refresh-state sequence")


def capture_fallback_presence(args: argparse.Namespace) -> int:
    session = _read_session(Path(args.session))
    if args.address != "litellm_fallback.minimal":
        raise HarnessError("fallback presence proof address is not the exact matrix target")
    paths = [Path(args.before_state), Path(args.after_state), Path(args.refresh_output), Path(args.delete_output)]
    for path in paths:
        require_regular_file(path)
    before = [item for item in _walk_resources(load_json(paths[0]).get("values", {}).get("root_module", {})) if item.get("address") == args.address]
    after = [item for item in _walk_resources(load_json(paths[1]).get("values", {}).get("root_module", {})) if item.get("address") == args.address]
    if len(before) != 1 or len(after) != 1:
        raise HarnessError("fallback presence proof does not contain one exact target before and after refresh")
    for item in (before[0], after[0]):
        if item.get("mode") != "managed" or item.get("type") != "litellm_fallback":
            raise HarnessError("fallback presence proof target metadata changed")
    before_values, after_values = before[0].get("values"), after[0].get("values")
    if not isinstance(before_values, dict) or before_values != after_values:
        raise HarnessError("fallback authoritative refresh changed or lost the exact target")
    if any(not before_values.get(key) for key in ("id", "model", "fallback_type")):
        raise HarnessError("fallback presence proof lacks exact nonempty identity fields")
    commands = [item for item in _ledger_values(session) if item.get("record_type") == "command"]
    refresh_command = _command_for_output(commands, paths[2], require_failed=False)
    delete_command = _command_for_output(commands, paths[3], require_failed=True)
    state_command = _command_for_output(commands, paths[1], require_failed=False)
    expected_refresh = [args.cli, "apply", "-refresh-only", "-auto-approve", *args.refresh_argument]
    expected_delete = [args.cli, "destroy", "-auto-approve", *args.delete_argument]
    expected_state = [args.cli, "show", "-json"]
    if delete_command.get("exit_code") != 1:
        raise HarnessError("fallback delete proof is not a provider diagnostic exit")
    _require_consecutive_fallback_commands(
        commands, delete_command, refresh_command, state_command,
        _command_digest(expected_delete), _command_digest(expected_refresh), _command_digest(expected_state),
    )
    bindings = {key: session[key] for key in (
        "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256",
    )}
    item = {
        "record_type": "phase", **bindings, "phase": "fallback-authoritative-presence",
        "subjects": ["litellm_fallback"], "target_code": "fallback-minimal",
        "command_sha256": refresh_command["command_sha256"],
        "result_sha256": refresh_command["result_sha256"], "command_exit_code": refresh_command["exit_code"],
        "delete_command_sha256": delete_command["command_sha256"],
        "delete_result_sha256": delete_command["result_sha256"], "delete_exit_code": delete_command["exit_code"],
        "state_command_sha256": state_command["command_sha256"],
        "state_result_sha256": state_command["result_sha256"], "state_exit_code": state_command["exit_code"],
        "assertion_sha256": _assertion_digest(paths, "terraform-plan-state-api"),
    }
    item["receipt_hmac"] = _sign_record(item, _session_key(session))
    _append_ledger(Path(session["ledger"]), item)
    return 0


def observe_smoke(args: argparse.Namespace) -> int:
    session = _read_session(Path(args.session))
    fallback_artifacts = (args.fallback_delete_evidence, args.fallback_presence_state, args.fallback_presence_output)
    if args.fallback_delete_unconfirmed != all(bool(value) for value in fallback_artifacts) or (not args.fallback_delete_unconfirmed and any(bool(value) for value in fallback_artifacts)):
        raise HarnessError("fallback diagnostic and authoritative presence artifacts must be complete and paired")
    if args.fallback_delete_unconfirmed and args.fallback_delete_success_evidence:
        raise HarnessError("fallback deletion cannot be both confirmed and unconfirmed")
    if bool(args.fallback_delete_success_evidence) != bool(args.fallback_delete_success_cli):
        raise HarnessError("successful fallback delete evidence and CLI must be paired")
    paths = [Path(args.plan), Path(args.refresh_state), Path(args.state), Path(args.steady_plan), Path(args.final_state)]
    for path in paths:
        require_regular_file(path)
    plan, raw_refresh, state, steady = (load_json(paths[index]) for index in range(4))
    if paths[4].read_text(encoding="utf-8").strip():
        raise HarnessError("smoke cleanup did not produce empty Terraform state")
    steady_changes = steady.get("resource_changes", [])
    if any(change.get("change", {}).get("actions") not in ([], ["no-op"], ["read"]) for change in steady_changes):
        raise HarnessError("smoke evidence contains post-refresh drift")
    state_resources = list(_walk_resources(state.get("values", {}).get("root_module", {})))
    managed = {
        item.get("type") for item in state_resources
        if item.get("mode") == "managed" and str(item.get("type", "")).startswith("litellm_")
    }
    configured = _configured_data_catalog(plan)
    refreshed = _raw_state_data_catalog(raw_refresh)
    state_data = {
        (item.get("address"), item.get("type")) for item in state_resources
        if item.get("mode") == "data"
    }
    if configured != refreshed or configured != state_data:
        raise HarnessError("complete refreshed state differs from its exact configured data-source catalog")
    data_sources = {resource_type for _, resource_type in configured}
    matrix = load_json(MATRIX_PATH)
    expected = _expected_subjects(matrix)
    resource_contracts = {entry["type"]: entry for entry in matrix["resources"]}
    if not managed or not managed.issubset(expected["lifecycle"]) or not data_sources.issubset(expected["data_source"]):
        raise HarnessError("smoke plan/state addresses are outside the checked matrix")
    if args.fallback_delete_unconfirmed and "litellm_fallback" not in managed:
        raise HarnessError("fallback delete diagnostic was attached to another smoke scenario")
    fallback_contract_present = "litellm_fallback" in managed and bool(resource_contracts["litellm_fallback"].get("lifecycle_skip_reason"))
    if fallback_contract_present and not (args.fallback_delete_unconfirmed or args.fallback_delete_success_evidence):
        raise HarnessError("fallback lifecycle lacks either confirmed deletion or exact retained-presence proof")
    fallback_success_command = None
    fallback_success_paths = []
    if args.fallback_delete_success_evidence:
        fallback_success_path = Path(args.fallback_delete_success_evidence)
        commands = [item for item in _ledger_values(session) if item.get("record_type") == "command"]
        fallback_success_command = _command_for_output(commands, fallback_success_path, require_failed=False)
        expected = [args.fallback_delete_success_cli, "destroy", "-auto-approve", *args.fallback_delete_success_argument]
        if fallback_success_command.get("command_sha256") != _command_digest(expected):
            raise HarnessError("successful fallback lifecycle is not bound to the exact destroy command")
        fallback_success_paths = [fallback_success_path]
    fallback_presence_paths = []
    if args.fallback_delete_unconfirmed:
        fallback_presence_paths = [paths[2], Path(args.fallback_presence_state), Path(args.fallback_presence_output)]
        phase_digest = _assertion_digest([*fallback_presence_paths, Path(args.fallback_delete_evidence)], "terraform-plan-state-api")
        phases = [
            item for item in _ledger_values(session)
            if item.get("record_type") == "phase"
            and item.get("phase") == "fallback-authoritative-presence"
            and item.get("assertion_sha256") == phase_digest
        ]
        if len(phases) != 1 or phases[0].get("subjects") != ["litellm_fallback"]:
            raise HarnessError("fallback delete skip lacks one exact authoritative presence phase")
    for subject in sorted(managed):
        for category in ("resource_coverage", "lifecycle", "drift"):
            reason = ""
            diagnostic_code = ""
            status = "passed"
            if category == "lifecycle" and args.fallback_delete_unconfirmed:
                reason = resource_contracts[subject].get("lifecycle_skip_reason", "")
                diagnostic_code = resource_contracts[subject].get("lifecycle_skip_diagnostic_code", "")
                status = "skipped" if reason else "passed"
            lifecycle_success = category == "lifecycle" and subject == "litellm_fallback" and fallback_success_command is not None
            _append_scenario(
                session, name=f"{category}:{subject}", category=category,
                status=status, reason=reason, diagnostic_code=diagnostic_code,
                assertion="terraform-plan-state-api",
                evidence_paths=[*paths, *fallback_presence_paths] if diagnostic_code else [*paths, *fallback_success_paths] if lifecycle_success else paths,
                command_record=fallback_success_command if lifecycle_success else None,
                diagnostic_evidence=Path(args.fallback_delete_evidence) if diagnostic_code else None,
            )
    if data_sources:
        phase_digest = _assertion_digest(paths[:2], "refresh-only-config-state-zero-drift")
        phases = [item for item in _ledger_values(session) if item.get("record_type") == "phase" and item.get("assertion_sha256") == phase_digest]
        if len(phases) != 1 or set(phases[0].get("subjects", [])) != data_sources:
            raise HarnessError("data-source completion lacks one exact refresh-only phase")
        for subject in sorted(data_sources):
            _append_scenario(
                session, name=f"data_source:{subject}", category="data_source", status="passed",
                reason="", diagnostic_code="", assertion="refresh-only-config-state-zero-drift",
                evidence_paths=paths, command_record=phases[0],
            )
    return 0


def finalize_evidence(args: argparse.Namespace) -> int:
    session = _read_session(Path(args.session))
    values = _ledger_values(session)
    allowed_command = {
        "record_type", "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256", "command_sha256",
        "result_sha256", "exit_code", "output_bytes", "receipt_hmac",
    }
    allowed_scenario = {
        "record_type", "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256", "name", "subject",
        "category", "status", "assertion", "command_sha256", "result_sha256",
        "command_exit_code", "assertion_sha256", "reason", "diagnostic_code", "receipt_hmac",
    }
    allowed_phase = {
        "record_type", "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256", "phase", "subjects",
        "command_sha256", "result_sha256", "command_exit_code", "assertion_sha256", "receipt_hmac",
        "target_code", "delete_command_sha256", "delete_result_sha256", "delete_exit_code",
        "state_command_sha256", "state_result_sha256", "state_exit_code",
    }
    fallback_phase_fields = {
        "target_code", "delete_command_sha256", "delete_result_sha256", "delete_exit_code",
        "state_command_sha256", "state_result_sha256", "state_exit_code",
    }
    bindings = {key: session[key] for key in (
        "run_nonce", "cli_lane", "candidate_commit", "provider_sha256",
        "provider_schema_sha256", "harness_sha256", "matrix_sha256",
    )}
    command_pairs = set()
    scenarios = []
    for item in values[1:]:
        kind = item.get("record_type")
        allowed = allowed_command if kind == "command" else allowed_scenario if kind == "scenario" else allowed_phase if kind == "phase" else set()
        required = allowed_command if kind == "command" else allowed_scenario - {"reason", "diagnostic_code"} if kind == "scenario" else allowed_phase - fallback_phase_fields if kind == "phase" else set()
        if not allowed or set(item) - allowed or not required.issubset(item):
            raise HarnessError("evidence ledger record schema is invalid")
        if any(item.get(key) != value for key, value in bindings.items()):
            raise HarnessError("evidence ledger record binding changed")
        digest_names = ("command_sha256", "result_sha256") if kind == "command" else ("command_sha256", "result_sha256", "assertion_sha256")
        if any(not re.fullmatch(r"[0-9a-f]{64}", str(item.get(key, ""))) for key in digest_names):
            raise HarnessError("evidence ledger record digest is invalid")
        if kind == "command":
            if not isinstance(item["exit_code"], int) or not -255 <= item["exit_code"] <= 126 or not isinstance(item["output_bytes"], int) or not 0 <= item["output_bytes"] <= 10 * 1024 * 1024 + 65536:
                raise HarnessError("command receipt result bound is invalid")
            command_pairs.add((item["command_sha256"], item["result_sha256"], item["exit_code"]))
        elif kind == "phase":
            common_phase_invalid = (
                item.get("command_exit_code") != 0
                or (item["command_sha256"], item["result_sha256"], item["command_exit_code"]) not in command_pairs
                or not isinstance(item.get("subjects"), list)
                or item["subjects"] != sorted(set(item["subjects"]))
            )
            if item.get("phase") == "refresh-only-data-sources":
                phase_invalid = not set(item["subjects"]).issubset(_expected_subjects(load_json(MATRIX_PATH))["data_source"])
            elif item.get("phase") == "fallback-authoritative-presence":
                phase_invalid = (
                    item["subjects"] != ["litellm_fallback"]
                    or item.get("target_code") != "fallback-minimal"
                    or not fallback_phase_fields.issubset(item)
                    or item.get("delete_exit_code") != 1
                    or item.get("state_exit_code") != 0
                    or (item.get("delete_command_sha256"), item.get("delete_result_sha256"), item.get("delete_exit_code")) not in command_pairs
                    or (item.get("state_command_sha256"), item.get("state_result_sha256"), item.get("state_exit_code")) not in command_pairs
                )
            else:
                phase_invalid = True
            if common_phase_invalid or phase_invalid:
                raise HarnessError("supervised phase evidence is invalid")
        else:
            if item.get("status") not in STATUSES or item.get("category") not in CATEGORIES or item.get("assertion") not in ASSERTION_CODES:
                raise HarnessError("scenario evidence enum is invalid")
            if (item["command_sha256"], item["result_sha256"], item["command_exit_code"]) not in command_pairs:
                raise HarnessError("scenario evidence does not reference an executed command")
            if item["status"] == "skipped" and not item.get("reason"):
                raise HarnessError("scenario evidence skip lacks an exact reason")
            if item["status"] != "skipped" and item.get("reason"):
                raise HarnessError("scenario evidence non-skip has a reason")
            if item.get("diagnostic_code") and item["diagnostic_code"] not in DIAGNOSTIC_CODES:
                raise HarnessError("scenario evidence diagnostic enum is invalid")
            scenarios.append(item)
    matrix = load_json(MATRIX_PATH)
    identities = [(item["category"], item["subject"]) for item in scenarios]
    if len(identities) != len(set(identities)):
        raise HarnessError("trusted evidence ledger contains duplicate scenario subjects")
    for category, subjects in _expected_subjects(matrix).items():
        actual = {item["subject"] for item in scenarios if item["category"] == category}
        if actual != subjects:
            raise HarnessError("trusted evidence ledger lacks an exact scenario set")
    if len(scenarios) != 166:
        raise HarnessError("trusted evidence ledger does not contain the exact 166-scenario matrix")
    actual_skips = {
        (item["category"], item["subject"], item.get("reason", ""))
        for item in scenarios if item["status"] == "skipped"
    }
    lane = session["cli_lane"]
    if lane in {"terraform-1.11.4", "opentofu-1.11.1"}:
        if not MODERN_MANDATORY_SKIPS.issubset(actual_skips) or actual_skips - MODERN_MANDATORY_SKIPS - FALLBACK_CONDITIONAL_SKIPS:
            raise HarnessError("modern CLI evidence differs from its exact mandatory/conditional skip contract")
    elif lane in {"terraform-1.1.0", "opentofu-1.6.3"}:
        agent_skips = actual_skips & PRE_111_AGENT_SKIPS
        if len(agent_skips) != 1 or not PRE_111_MANDATORY_SKIPS.issubset(actual_skips) or actual_skips - PRE_111_MANDATORY_SKIPS - agent_skips - FALLBACK_CONDITIONAL_SKIPS:
            raise HarnessError("pre-1.11 CLI evidence differs from its exact lane/subject/reason skip contract")
    else:
        raise HarnessError("evidence CLI lane is outside the exact release matrix")
    if sum(item["status"] == "passed" for item in scenarios) != 166 - len(actual_skips):
        raise HarnessError("scenario pass/skip accounting is not exact")
    report_scenarios = []
    evidence_codes = {
        "resource_coverage": "apply-refresh-plan-destroy", "lifecycle": "apply-refresh-plan-destroy",
        "drift": "apply-refresh-plan-destroy", "data_source": "apply-refresh-plan",
        "upgrade": "upgrade-refresh-plan", "import": "import-refresh-apply-plan-detach",
        "replacement": "replacement-plan-apply", "failure_recovery": "fault-retry-converged",
        "optional_feature": "apply-refresh-plan-destroy", "documentation": "documentation-validated",
    }
    for item in scenarios:
        value = {key: item[key] for key in ("name", "subject", "category", "status")}
        value["evidence_code"] = evidence_codes[item["category"]]
        if (
            item["category"] == "upgrade"
            and item.get("assertion") == "upgrade-private-plan-trigger-migration"
        ):
            value["evidence_code"] = "upgrade-reviewed-private-migration"
        if item.get("reason"):
            value["reason"] = item["reason"]
            value["evidence_code"] = {
                "enterprise-license-required": "enterprise-unavailable",
                "cli-version-below-1.11": "cli-feature-unavailable",
                "previous-release-resource-unavailable": "previous-release-unavailable",
            }.get(item["reason"], "api-unavailable")
        if item.get("diagnostic_code"):
            value["diagnostic_code"] = item["diagnostic_code"]
        report_scenarios.append(value)
    previous = load_json(TOOLS_PATH)["previous_provider"]
    previous_archive = previous["archives"][platform_key()]
    previous_executable_digest, previous_schema_digest = provider_schema_fingerprint(
        args.cli, Path(args.previous_provider_binary), validate_upgrade_contract=False
    )
    expected_previous_schema = previous["schema_sha256_by_cli"].get(session["cli_lane"])
    if previous_executable_digest != previous_archive["executable_sha256"] or previous_schema_digest != expected_previous_schema:
        raise HarnessError("executed previous provider differs from its signed exact CLI-bound pin")
    ledger_digest = hash_file(Path(session["ledger"]))
    provenance = {
        "cli_product": session["cli_lane"].split("-", 1)[0],
        "cli_version": session["cli_lane"].split("-", 1)[1],
        "cli_executable_sha256": hash_file(Path(args.cli)),
        "provider_executable_sha256": session["provider_sha256"],
        "provider_schema_sha256": session["provider_schema_sha256"],
        "candidate_commit": session["candidate_commit"], "harness_sha256": session["harness_sha256"],
        "matrix_sha256": session["matrix_sha256"], "evidence_ledger_sha256": ledger_digest,
        "run_nonce_sha256": session["run_nonce"],
        "previous_signature_sha256": previous["signature_sha256"],
        "previous_archive_sha256": previous_archive["sha256"],
        "previous_executable_sha256": previous_executable_digest,
        "previous_provider_schema_sha256": previous_schema_digest,
        "previous_manifest_sha256": previous["manifest_sha256"],
        "previous_signing_fingerprint": previous["signing_key"]["fingerprint"],
    }
    report = {"schema_version": REPORT_SCHEMA_VERSION, "mode": "destructive-local", "summary": summarize_results(report_scenarios), "scenarios": report_scenarios, "provenance": provenance}
    # The ledger contains only controlled labels, bounded integers, and digests;
    # raw Terraform/API output remains in the private scratch log.
    ledger_encoded = Path(session["ledger"]).read_bytes()
    publish_execution_artifacts(Path(args.report), Path(args.evidence_report), report, ledger_encoded)
    print(f"Execution report passed trusted evidence validation: emitted={len(report_scenarios)} commands={sum(item.get('record_type') == 'command' for item in values)}")
    return 0


def remove_session_key(args: argparse.Namespace) -> int:
    session_path = Path(args.session).absolute()
    session = _read_session(session_path)
    key_path = Path(session["key_file"]).absolute()
    if key_path.parent != session_path.parent or not key_path.name.startswith(".ledger-key-"):
        raise HarnessError("evidence signing key is outside its private session directory")
    with secure_parent_fd(key_path) as (directory_fd, name):
        descriptor = os.open(name, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0), dir_fd=directory_fd)
        try:
            info = os.fstat(descriptor)
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_size != 32:
                raise HarnessError("evidence signing key changed before removal")
        finally:
            os.close(descriptor)
        os.unlink(name, dir_fd=directory_fd)
        os.fsync(directory_fd)
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
    canonical = commands.add_parser("canonical-path")
    canonical.add_argument("--path", required=True)
    canonical.set_defaults(function=canonical_path)
    private_temp = commands.add_parser("make-private-temp")
    private_temp.add_argument("--base", required=True)
    private_temp.set_defaults(function=make_private_temp)
    bundle = commands.add_parser("prepare-provider")
    bundle.add_argument("--provider-binary", required=True)
    bundle.add_argument("--directory", required=True)
    bundle.set_defaults(function=prepare_provider)
    session = commands.add_parser("start-session")
    session.add_argument("--ledger", required=True)
    session.add_argument("--session", required=True)
    session.add_argument("--cli", required=True)
    session.add_argument("--provider-binary", required=True)
    session.set_defaults(function=start_session)
    observation = commands.add_parser("record-observation")
    observation.add_argument("--session", required=True)
    observation.add_argument("--name", required=True)
    observation.add_argument("--category", required=True)
    observation.add_argument("--status", required=True, choices=tuple(sorted(STATUSES)))
    observation.add_argument("--reason")
    observation.add_argument("--diagnostic-code")
    observation.add_argument("--diagnostic-evidence")
    observation.add_argument("--fallback-presence-evidence", nargs=3)
    observation.add_argument("--assertion", required=True)
    observation.add_argument("--evidence", action="append", required=True)
    observation.set_defaults(function=record_observation)
    refresh = commands.add_parser("capture-refresh-phase")
    refresh.add_argument("--session", required=True)
    refresh.add_argument("--plan", required=True)
    refresh.add_argument("--refresh-state", required=True)
    refresh.add_argument("--cli", required=True)
    refresh.add_argument("--refresh-argument", action="append", default=[])
    refresh.set_defaults(function=capture_refresh_phase)
    smoke = commands.add_parser("observe-smoke")
    smoke.add_argument("--session", required=True)
    smoke.add_argument("--plan", required=True)
    smoke.add_argument("--refresh-state", required=True)
    smoke.add_argument("--state", required=True)
    smoke.add_argument("--steady-plan", required=True)
    smoke.add_argument("--final-state", required=True)
    smoke.add_argument("--fallback-delete-unconfirmed", action="store_true")
    smoke.add_argument("--fallback-delete-evidence")
    smoke.add_argument("--fallback-presence-state")
    smoke.add_argument("--fallback-presence-output")
    smoke.add_argument("--fallback-delete-success-evidence")
    smoke.add_argument("--fallback-delete-success-cli")
    smoke.add_argument("--fallback-delete-success-argument", action="append", default=[])
    smoke.set_defaults(function=observe_smoke)
    fallback_presence = commands.add_parser("capture-fallback-presence")
    fallback_presence.add_argument("--session", required=True)
    fallback_presence.add_argument("--before-state", required=True)
    fallback_presence.add_argument("--after-state", required=True)
    fallback_presence.add_argument("--refresh-output", required=True)
    fallback_presence.add_argument("--delete-output", required=True)
    fallback_presence.add_argument("--address", required=True)
    fallback_presence.add_argument("--cli", required=True)
    fallback_presence.add_argument("--refresh-argument", action="append", default=[])
    fallback_presence.add_argument("--delete-argument", action="append", default=[])
    fallback_presence.set_defaults(function=capture_fallback_presence)
    finalize = commands.add_parser("finalize-evidence")
    finalize.add_argument("--session", required=True)
    finalize.add_argument("--report", required=True)
    finalize.add_argument("--evidence-report", required=True)
    finalize.add_argument("--cli", required=True)
    finalize.add_argument("--previous-provider-binary", required=True)
    finalize.set_defaults(function=finalize_evidence)
    remove_key = commands.add_parser("remove-session-key")
    remove_key.add_argument("--session", required=True)
    remove_key.set_defaults(function=remove_session_key)
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
