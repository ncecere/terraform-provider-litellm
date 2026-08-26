import importlib.util
import json
import os
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "harness.py"
SPEC = importlib.util.spec_from_file_location("upgrade_harness", MODULE_PATH)
assert SPEC and SPEC.loader
harness = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(harness)


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
        value = harness.redact(
            "api_key=sk-thismustnotescape endpoint=https://host.invalid/path id=remote-123"
        )
        self.assertNotIn("sk-thismustnotescape", value)
        self.assertNotIn("https://", value)
        self.assertNotIn("remote-123", value)

    def test_import_cleanup_is_state_rm_only(self):
        commands = harness.import_cleanup_commands(
            ["litellm_model.imported", "litellm_team_member.imported"]
        )
        self.assertTrue(commands)
        for command in commands:
            self.assertEqual(command[:2], ["state", "rm"])
            self.assertNotIn("destroy", command)

    def test_unexplained_skip_fails_closed(self):
        with self.assertRaises(harness.HarnessError):
            harness.account_skips(
                [{"status": "skipped", "reason": "someone-disabled-it"}],
                {"enterprise-license-required"},
            )
        harness.account_skips(
            [{"status": "skipped", "reason": "enterprise-license-required"}],
            {"enterprise-license-required"},
        )

    def test_replacement_ids_compared_without_returning_values(self):
        with tempfile.TemporaryDirectory() as raw:
            old = Path(raw) / "old"
            new = Path(raw) / "new"
            old.write_text("private-old-id", encoding="utf-8")
            new.write_text("private-new-id", encoding="utf-8")
            self.assertFalse(harness.compare_private_ids(old, new, os.urandom(32)))
            new.write_text("private-old-id", encoding="utf-8")
            self.assertTrue(harness.compare_private_ids(old, new, b"fixed-test-key"))

    def test_cleanup_failure_is_not_suppressed(self):
        with self.assertRaises(harness.HarnessError):
            harness.require_cleanup_success([0, 1, 0])
        harness.require_cleanup_success([0, 0])

    def test_release_and_tool_versions_are_checksum_pinned(self):
        harness.check_release_contract(harness.load_json(harness.TOOLS_PATH))

    def test_report_rejects_protected_values(self):
        with tempfile.TemporaryDirectory() as raw:
            with self.assertRaises(harness.HarnessError):
                harness.write_report(
                    Path(raw) / "report.json",
                    {
                        "schema_version": 1,
                        "mode": "test",
                        "summary": {},
                        "scenarios": [{"diagnostic_titles": ["https://private.invalid"]}],
                        "tools": {},
                    },
                )

    def test_matrix_json_is_stable_machine_readable_json(self):
        with harness.MATRIX_PATH.open(encoding="utf-8") as handle:
            value = json.load(handle)
        self.assertEqual(value["schema_version"], 1)


if __name__ == "__main__":
    unittest.main()
