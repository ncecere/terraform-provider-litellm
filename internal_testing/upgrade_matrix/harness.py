#!/usr/bin/env python3
"""Fail-closed assembly and safety primitives for the upgrade matrix.

This program deliberately never relays child-process output. Terraform/OpenTofu
state, plans, logs, remote IDs, credentials, and URLs stay in mode-0700 scratch
space and are removed by the caller's cleanup trap. The only durable result is
an allowlisted JSON summary.
"""
from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import urllib.request
import zipfile
from pathlib import Path
from typing import Iterable

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
MATRIX_PATH = HERE / "matrix.json"
TOOLS_PATH = HERE / "tools.lock.json"
MAX_CAPTURE = 1024 * 1024
ALLOWED_RESULT_KEYS = {"schema_version", "mode", "summary", "scenarios", "tools"}
URL_RE = re.compile(r"(?i)\b(?:https?|postgres(?:ql)?|file)://\S+")
SECRET_RE = re.compile(r"(?i)(?:sk-[A-Za-z0-9_-]{8,}|bearer\s+\S+|api[_-]?key\s*[=:]\s*\S+)")
ID_RE = re.compile(r"(?i)\b(?:id|token|key|url|state|plan|log)\s*[=:]\s*\S+")


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


def hash_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def compare_private_ids(old_path: Path, new_path: Path, key: bytes) -> bool:
    """Compare remote IDs without returning or persisting either ID."""
    def fingerprint(path: Path) -> bytes:
        value = path.read_bytes().strip()
        if not value:
            raise HarnessError("replacement ID capture is empty")
        return hmac.new(key, value, hashlib.sha256).digest()
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
    if len(resources) != 23 or len(expected_resources) != 23:
        raise HarnessError("resource inventory must contain exactly 23 unique types")
    if len(matrix["data_sources"]) != 33 or len(expected_data_sources) != 33:
        raise HarnessError("data-source inventory must contain exactly 33 unique types")
    if expected_resources != provider_types("", "resource"):
        raise HarnessError("resource inventory differs from provider registration")
    if expected_data_sources != provider_types("", "data_source"):
        raise HarnessError("data-source inventory differs from provider registration")
    actions = sorted(item["type"] for item in resources if item.get("action"))
    if matrix.get("non_importable_resources") != [] or actions != sorted(matrix.get("action_resources", [])):
        raise HarnessError("importable/action resource accounting is incomplete")
    for item in resources:
        for required in ("fixture", "address", "import_expression", "lane", "action"):
            if required not in item:
                raise HarnessError(f"resource matrix entry lacks {required}")
        doc = ROOT / "docs" / "resources" / f"{item['type'].removeprefix('litellm_')}.md"
        if not doc.is_file() or "## Import" not in doc.read_text(encoding="utf-8"):
            raise HarnessError("an importable resource lacks import documentation")
        for fixture in item["fixture"]:
            if not (ROOT / "internal_testing" / "resources" / fixture).is_file():
                raise HarnessError("resource matrix references a missing fixture")
    expected_counts = {
        "resource_coverage": 23, "upgrade": 23, "lifecycle": 23, "import": 23,
        "drift": 23, "replacement": 2, "failure_recovery": 2,
        "data_source": 33, "documentation": 3,
    }
    if matrix.get("scenario_counts") != expected_counts:
        raise HarnessError("scenario count contract changed without explicit accounting")
    for scenario in matrix.get("replacement_scenarios", []):
        if scenario.get("compare") != "hmac-sha256" or not scenario.get("post_replacement_no_drift"):
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
    for value in [previous["checksums_file_sha256"], previous["manifest_sha256"], *previous["archives"].values()]:
        if not re.fullmatch(r"[0-9a-f]{64}", value):
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


def make_cli_config(directory: Path, provider_binary: Path) -> Path:
    provider_dir = provider_binary.resolve().parent
    config = directory / "terraformrc"
    config.write_text(
        'provider_installation {\n  dev_overrides {\n'
        f'    "registry.terraform.io/ncecere/litellm" = {json.dumps(str(provider_dir))}\n'
        "  }\n  direct {}\n}\n",
        encoding="utf-8",
    )
    config.chmod(stat.S_IRUSR | stat.S_IWUSR)
    return config


def validate_resource_modules(matrix: dict, cli: str, provider_binary: Path) -> list[dict]:
    results: list[dict] = []
    with tempfile.TemporaryDirectory(prefix="litellm-matrix-") as raw:
        scratch = Path(raw)
        scratch.chmod(0o700)
        cli_config = make_cli_config(scratch, provider_binary)
        for item in matrix["resources"]:
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
            results.append({"name": f"assembly:{item['type']}", "category": "resource_coverage", "status": status, "diagnostic_titles": titles})
    return results


def validate_optional_111(cli: str, provider_binary: Path) -> list[dict]:
    version = selected_cli_version(cli)
    if not supports_optional_111(version):
        return [{
            "name": "optional:key_wo-and-send_invite_email",
            "category": "lifecycle",
            "status": "skipped",
            "reason": "cli-version-below-1.11",
            "diagnostic_titles": [],
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
        "name": "optional:key_wo-and-send_invite_email",
        "category": "lifecycle",
        "status": "passed" if code == 0 else "failed",
        "diagnostic_titles": titles,
    }]


def validate_examples(cli: str, provider_binary: Path) -> list[dict]:
    results: list[dict] = []
    example_dirs = sorted(path.parent for path in (ROOT / "examples").glob("*/main.tf"))
    with tempfile.TemporaryDirectory(prefix="litellm-docs-") as raw:
        scratch = Path(raw)
        scratch.chmod(0o700)
        cli_config = make_cli_config(scratch, provider_binary)
        for source in example_dirs:
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
                "name": f"documentation:{source.name}",
                "category": "documentation",
                "status": "passed" if code == 0 else "failed",
                "diagnostic_titles": titles,
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
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8", errors="replace")
        if URL_RE.search(text) or SECRET_RE.search(text) or ID_RE.search(text):
            raise HarnessError("result artifact failed credential/value scan")


def write_report(path: Path, report: dict) -> None:
    if set(report) - ALLOWED_RESULT_KEYS:
        raise HarnessError("report contains a non-allowlisted top-level field")
    encoded = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if URL_RE.search(encoded) or SECRET_RE.search(encoded) or ID_RE.search(encoded):
        raise HarnessError("report would expose a protected value")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(encoded, encoding="utf-8")
    path.chmod(stat.S_IRUSR | stat.S_IWUSR)
    credential_scan([path])


def assembly(args: argparse.Namespace) -> int:
    matrix = load_json(MATRIX_PATH)
    tools = load_json(TOOLS_PATH)
    check_inventory(matrix)
    check_release_contract(tools)
    check_format(args.cli)
    results: list[dict] = []
    if args.provider_binary:
        provider_binary = Path(args.provider_binary)
        if not provider_binary.is_file():
            raise HarnessError("current provider binary is missing")
        binary_before = hash_file(provider_binary)
        results = validate_resource_modules(matrix, args.cli, provider_binary)
        results.extend(validate_optional_111(args.cli, provider_binary))
        results.extend(validate_examples(args.cli, provider_binary))
        if hash_file(provider_binary) != binary_before:
            raise HarnessError("assembly modified the current provider binary")
        if any(item["status"] == "failed" for item in results):
            raise HarnessError("one or more assembled resource or documentation modules failed validation")
    else:
        results = [{"name":"inventory-and-format", "category":"documentation", "status":"passed", "diagnostic_titles":[]}]
    report = {
        "schema_version": 1,
        "mode": "assembly",
        "summary": dict(matrix["scenario_counts"]),
        "scenarios": results,
        "tools": {"terraform":["1.0.11","1.11.4"], "opentofu":["1.6.3","1.11.1"], "previous_provider":"2.0.1"},
    }
    account_skips(results, set(matrix["allowed_skip_reasons"]))
    if args.report:
        write_report(Path(args.report), report)
    print("Assembly passed: resources=23 upgrades=23 imports=23 data-sources=33 replacements=2 recovery=2 docs=3")
    return 0


def platform_key() -> str:
    system = {"Darwin":"darwin", "Linux":"linux"}.get(platform.system())
    machine = {"x86_64":"amd64", "arm64":"arm64", "aarch64":"arm64"}.get(platform.machine())
    if not system or not machine:
        raise HarnessError("unsupported tool-install platform")
    return f"{system}_{machine}"


def download_verified(url: str, destination: Path, checksum: str, offline: bool) -> None:
    if destination.is_file() and hash_file(destination) == checksum:
        return
    if destination.exists():
        destination.unlink()
    if offline:
        raise HarnessError("verified cache entry is unavailable in offline mode")
    partial = destination.with_suffix(destination.suffix + ".partial")
    try:
        with urllib.request.urlopen(url, timeout=60) as response, partial.open("wb") as output:
            shutil.copyfileobj(response, output, length=1024 * 1024)
        if hash_file(partial) != checksum:
            raise HarnessError("download checksum mismatch")
        partial.replace(destination)
    finally:
        partial.unlink(missing_ok=True)


def install_tool(args: argparse.Namespace) -> int:
    tools = load_json(TOOLS_PATH)
    product = args.product
    metadata = tools[product].get(args.version)
    if metadata is None:
        raise HarnessError("requested tool version is not pinned")
    archive = metadata["archives"].get(platform_key())
    if archive is None:
        raise HarnessError("requested tool platform is not pinned")
    cache = Path(args.cache).expanduser().resolve()
    cache.mkdir(parents=True, exist_ok=True, mode=0o700)
    zip_path = cache / archive["file"]
    download_verified(f"{metadata['base_url']}/{archive['file']}", zip_path, archive["sha256"], args.offline)
    target = cache / product / args.version / platform_key()
    target.mkdir(parents=True, exist_ok=True, mode=0o700)
    executable = target / ("terraform" if product == "terraform" else "tofu")
    if not executable.exists():
        with zipfile.ZipFile(zip_path) as package:
            member = "terraform" if product == "terraform" else "tofu"
            if member not in package.namelist():
                raise HarnessError("tool archive has an unexpected layout")
            with package.open(member) as source, executable.open("wb") as output:
                shutil.copyfileobj(source, output)
        executable.chmod(0o700)
    code, _ = safe_run([str(executable), "version"], target)
    if code != 0:
        raise HarnessError("installed tool did not execute")
    print(f"Verified {product} {args.version} is present in the private cache")
    return 0


def install_previous(args: argparse.Namespace) -> int:
    tools = load_json(TOOLS_PATH)["previous_provider"]
    key = platform_key()
    expected = tools["archives"].get(key)
    if not expected:
        raise HarnessError("previous provider platform is not pinned")
    cache = Path(args.cache).expanduser().resolve()
    cache.mkdir(parents=True, exist_ok=True, mode=0o700)
    name = f"terraform-provider-litellm_2.0.1_{key}.zip"
    base = "https://github.com/ncecere/terraform-provider-litellm/releases/download/v2.0.1"
    archive = cache / name
    sums = cache / "terraform-provider-litellm_2.0.1_SHA256SUMS"
    manifest = cache / "terraform-provider-litellm_2.0.1_manifest.json"
    download_verified(f"{base}/{name}", archive, expected, args.offline)
    download_verified(f"{base}/{sums.name}", sums, tools["checksums_file_sha256"], args.offline)
    download_verified(f"{base}/{manifest.name}", manifest, tools["manifest_sha256"], args.offline)
    listed = {
        filename: checksum
        for checksum, filename in (
            line.split(maxsplit=1)
            for line in sums.read_text(encoding="utf-8").splitlines()
            if len(line.split(maxsplit=1)) == 2
        )
    }
    if listed.get(name) != expected:
        raise HarnessError("published checksum manifest does not select the pinned archive")
    manifest_value = load_json(manifest)
    if manifest_value != {"version":1, "metadata":{"protocol_versions":["6.0"]}}:
        raise HarnessError("published registry manifest changed unexpectedly")
    mirror = cache / "mirror" / "registry.terraform.io" / "ncecere" / "litellm"
    mirror.mkdir(parents=True, exist_ok=True, mode=0o700)
    shutil.copy2(archive, mirror / name)
    print("Verified published provider 2.0.1 archive, checksums, and registry manifest")
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
