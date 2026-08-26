import importlib.util
import json
import os
import stat
import tempfile
import threading
import time
import unittest
import zipfile
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "harness.py"
SPEC = importlib.util.spec_from_file_location("upgrade_harness", MODULE_PATH)
assert SPEC and SPEC.loader
harness = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(harness)


def valid_report(scenarios=None):
    scenarios = scenarios or []
    return {
        "schema_version": harness.REPORT_SCHEMA_VERSION,
        "mode": "assembly",
        "summary": harness.summarize_results(scenarios),
        "scenarios": scenarios,
        "provenance": {},
    }


class HarnessTests(unittest.TestCase):
    def test_inventory_and_assembly_contract(self):
        matrix = harness.load_json(harness.MATRIX_PATH)
        harness.check_inventory(matrix)
        self.assertEqual(len(matrix["resources"]), 23)
        self.assertEqual(len(matrix["data_sources"]), 33)
        self.assertEqual(sum(bool(item["action"]) for item in matrix["resources"]), 2)

    def test_version_selection_gates_write_only_features(self):
        self.assertFalse(harness.supports_optional_111("Terraform v1.0.11"))
        self.assertFalse(harness.supports_optional_111("OpenTofu v1.6.3"))
        self.assertTrue(harness.supports_optional_111("Terraform v1.11.4"))
        self.assertTrue(harness.supports_optional_111("OpenTofu v1.11.1"))

    def test_redaction_removes_secrets_urls_and_labeled_ids(self):
        value = harness.redact("api_key=sk-thismustnotescape endpoint=https://host.invalid/path id=remote-123")
        self.assertNotIn("sk-thismustnotescape", value)
        self.assertNotIn("https://", value)
        self.assertNotIn("remote-123", value)

    def test_import_cleanup_is_state_rm_only(self):
        commands = harness.import_cleanup_commands(["litellm_model.imported", "litellm_team_member.imported"])
        self.assertTrue(commands)
        for command in commands:
            self.assertEqual(command[:2], ["state", "rm"])
            self.assertNotIn("destroy", command)

    def test_unexplained_skip_fails_closed(self):
        with self.assertRaises(harness.HarnessError):
            harness.account_skips([{"status": "skipped", "reason": "someone-disabled-it"}], {"enterprise-license-required"})
        harness.account_skips([{"status": "skipped", "reason": "enterprise-license-required"}], {"enterprise-license-required"})

    def test_replacement_ids_compared_without_returning_values(self):
        with tempfile.TemporaryDirectory() as raw:
            old, new = Path(raw) / "old", Path(raw) / "new"
            old.write_text("private-old-id", encoding="utf-8")
            new.write_text("private-new-id", encoding="utf-8")
            self.assertFalse(harness.compare_private_ids(old, new, os.urandom(32)))
            new.write_text("private-old-id", encoding="utf-8")
            self.assertTrue(harness.compare_private_ids(old, new, b"fixed-test-key"))

    def test_cleanup_failure_is_not_suppressed(self):
        with self.assertRaises(harness.HarnessError):
            harness.require_cleanup_success([0, 1, 0])
        harness.require_cleanup_success([0, 0])

    def test_release_and_tool_versions_are_checksum_and_signature_pinned(self):
        tools = harness.load_json(harness.TOOLS_PATH)
        harness.check_release_contract(tools)
        key = tools["previous_provider"]["signing_key"]
        self.assertEqual(key["fingerprint"], "C753834A70062246C92CEF56F0A1AEC231353F8B")
        self.assertRegex(tools["previous_provider"]["signature_sha256"], r"^[0-9a-f]{64}$")

    def test_report_is_strict_derived_exclusive_and_create_only(self):
        scenario = {
            "name": "inventory:provider-types", "subject": "provider-types",
            "category": "inventory", "status": "passed", "evidence_code": "inventory-validated",
        }
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "report.json"
            report = valid_report([scenario])
            harness.write_report(path, report)
            loaded = json.loads(path.read_text())
            self.assertEqual(loaded["summary"], harness.summarize_results([scenario]))
            self.assertEqual(loaded["summary"]["upgrade"]["passed"], 0)
            with self.assertRaises(harness.HarnessError):
                harness.write_report(path, report)
            report["summary"]["upgrade"]["passed"] = 23
            with self.assertRaises(harness.HarnessError):
                harness.validate_report(report)

    def test_report_rejects_diagnostics_secrets_identity_urls_bodies_and_paths(self):
        injections = [
            ("diagnostic_title", "arbitrary child text"),
            ("password", "guessme"),
            ("client_secret", "value"),
            ("body", "response"),
            ("note", "https://private.invalid/x"),
            ("note", "550e8400-e29b-41d4-a716-446655440000"),
            ("note", "/tmp/private-state.tfstate"),
        ]
        for key, value in injections:
            report = valid_report()
            report["provenance"] = {key: value}
            with self.subTest(key=key, value=value), tempfile.TemporaryDirectory() as raw:
                with self.assertRaises(harness.HarnessError):
                    harness.write_report(Path(raw) / "report.json", report)

    def test_report_symlink_destination_is_never_followed(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            victim = root / "victim"
            victim.write_text("unchanged")
            (root / "report.json").symlink_to(victim)
            with self.assertRaises(harness.HarnessError):
                harness.write_report(root / "report.json", valid_report())
            self.assertEqual(victim.read_text(), "unchanged")

    def test_regular_file_checks_reject_symlink_and_hardlink_decoys(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            real = root / "real"
            real.write_text("x")
            link = root / "link"
            link.symlink_to(real)
            with self.assertRaises(harness.HarnessError):
                harness.hash_file(link)
            hard = root / "hard"
            os.link(real, hard)
            with self.assertRaises(harness.HarnessError):
                harness.hash_file(real)

    def test_malicious_archive_symlink_and_sibling_cache_are_rejected(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            archive = root / "bad.zip"
            with zipfile.ZipFile(archive, "w") as package:
                info = zipfile.ZipInfo("terraform")
                info.external_attr = (stat.S_IFLNK | 0o777) << 16
                package.writestr(info, "decoy")
            with self.assertRaises(harness.HarnessError):
                harness.extract_executable(archive, root / "out", "terraform", "0" * 64)

            good = root / "good.zip"
            with zipfile.ZipFile(good, "w") as package:
                package.writestr("terraform", b"binary")
            digest = __import__("hashlib").sha256(b"binary").hexdigest()
            destination = root / "cached"
            destination.mkdir(mode=0o700)
            executable = destination / "terraform"
            executable.write_bytes(b"binary")
            executable.chmod(0o700)
            (destination / "decoy").write_text("sibling")
            with self.assertRaises(harness.HarnessError):
                harness.extract_executable(good, destination, "terraform", digest)

    def test_provider_bundle_has_exactly_one_verified_executable_and_rejects_script(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            binary = root / "provider"
            binary.write_text("#!/bin/sh\nexit 0\n")
            binary.chmod(0o700)
            bundle, digest = harness.make_provider_bundle(root, binary)
            self.assertEqual(len(list(bundle.iterdir())), 1)
            self.assertEqual(harness.hash_file(next(bundle.iterdir())), digest)
            with self.assertRaises(harness.HarnessError):
                harness.provider_schema_fingerprint("/bin/sh", binary)

    def test_cache_lock_serializes_racing_installers(self):
        with tempfile.TemporaryDirectory() as raw:
            cache = harness.secure_directory(Path(raw).resolve() / "cache")
            entered, order = threading.Event(), []
            def first():
                with harness.cache_lock(cache):
                    order.append("first-enter")
                    entered.set()
                    time.sleep(0.15)
                    order.append("first-exit")
            def second():
                entered.wait()
                with harness.cache_lock(cache):
                    order.append("second-enter")
            a, b = threading.Thread(target=first), threading.Thread(target=second)
            a.start(); b.start(); a.join(); b.join()
            self.assertEqual(order, ["first-enter", "first-exit", "second-enter"])

    def test_matrix_json_is_stable_machine_readable_json(self):
        with harness.MATRIX_PATH.open(encoding="utf-8") as handle:
            value = json.load(handle)
        self.assertEqual(value["schema_version"], 1)


if __name__ == "__main__":
    unittest.main()
