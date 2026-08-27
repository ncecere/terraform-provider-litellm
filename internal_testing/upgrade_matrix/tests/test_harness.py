import hashlib
import importlib.util
import json
import os
import re
import stat
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import zipfile
from unittest import mock
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
        self.assertEqual(len(matrix["resources"]), 24)
        self.assertEqual(len(matrix["data_sources"]), 35)
        self.assertEqual(sum(matrix["scenario_counts"].values()) + len(matrix["optional_features"]), 166)
        self.assertEqual(len(matrix["terraform_1_11_4_expected_skips"]), 10)
        self.assertEqual(len(matrix["terraform_1_11_4_conditional_skips"]), 2)
        fallback = next(item for item in matrix["resources"] if item["type"] == "litellm_fallback")
        self.assertEqual(fallback["lifecycle_skip_reason"], "fallback-delete-not-authoritative")
        self.assertEqual(fallback["import_skip_reason"], "fallback-delete-not-authoritative")
        self.assertEqual(sum(bool(item["action"]) for item in matrix["resources"]), 2)
        self.assertEqual(
            matrix["upgrade_expected_private_plan_triggers"],
            {"litellm_agent": ["id"]},
        )
        nested = [
            (resource_type, path)
            for resource_type, paths in matrix["upgrade_expected_computed_migrations"].items()
            for path in paths if "." in path or "[*]" in path
        ]
        self.assertEqual(nested, [("litellm_team_member_add", "member[*].user_id")])
        self.assertEqual(matrix["upgrade_expected_representation_migrations"], {
            "litellm_agent": {
                "agent_card.signatures": "missing-to-empty-list-block",
                "agent_card.supports_authenticated_extended_card": "missing-to-null-bool",
            }
        })
        self.assertIn("upgrade-reviewed-private-migration", harness.EVIDENCE_CODES)
        self.assertIn(
            "upgrade-private-plan-trigger-migration", harness.ASSERTION_CODES
        )

    def test_diagnostic_evidence_binds_exact_failed_command_result(self):
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "diagnostic"
            payload = b"Error: reviewed\n"
            path.write_bytes(payload)
            exit_code = 1
            digest = hashlib.sha256(
                b"issue210-result-v1\0" + exit_code.to_bytes(4, "big", signed=True) + payload
            ).hexdigest()
            command = {"command_sha256": "1" * 64, "result_sha256": digest, "exit_code": exit_code}
            unrelated = {"command_sha256": "2" * 64, "result_sha256": "3" * 64, "exit_code": 1}
            self.assertIs(harness._command_for_diagnostic([unrelated, command], path), command)
            path.write_bytes(payload + b"extra")
            with self.assertRaises(harness.HarnessError):
                harness._command_for_diagnostic([unrelated, command], path)
            path.write_bytes(payload)
            command["exit_code"] = 0
            with self.assertRaises(harness.HarnessError):
                harness._command_for_diagnostic([command], path)
            success_digest = hashlib.sha256(
                b"issue210-result-v1\0" + (0).to_bytes(4, "big", signed=True) + payload
            ).hexdigest()
            success = {"command_sha256": "4" * 64, "result_sha256": success_digest, "exit_code": 0}
            self.assertIs(harness._command_for_output([success], path, require_failed=False), success)
            for failure_exit in (1, 22, 124, 130):  # API 500/401, timeout/connectivity, cancellation
                failed_digest = hashlib.sha256(
                    b"issue210-result-v1\0" + failure_exit.to_bytes(4, "big", signed=True) + payload
                ).hexdigest()
                failed = {"command_sha256": "5" * 64, "result_sha256": failed_digest, "exit_code": failure_exit}
                with self.assertRaises(harness.HarnessError):
                    harness._command_for_output([failed], path, require_failed=False)

    def test_fallback_presence_requires_exact_consecutive_command_sequence(self):
        old = {"command_sha256": "0" * 64}
        delete = {"command_sha256": "1" * 64}
        refresh = {"command_sha256": "2" * 64}
        state = {"command_sha256": "3" * 64}
        harness._require_consecutive_fallback_commands(
            [old, delete, refresh, state], delete, refresh, state,
            "1" * 64, "2" * 64, "3" * 64,
        )
        with self.assertRaises(harness.HarnessError):
            harness._require_consecutive_fallback_commands(
                [delete, old, refresh, state], delete, refresh, state,
                "1" * 64, "2" * 64, "3" * 64,
            )
        with self.assertRaises(harness.HarnessError):
            harness._require_consecutive_fallback_commands(
                [delete, refresh, state, old], delete, refresh, state,
                "1" * 64, "2" * 64, "3" * 64,
            )

    def test_private_migration_report_code_is_controlled_and_scan_safe(self):
        scenario = {
            "name": "upgrade:litellm_agent", "subject": "litellm_agent",
            "category": "upgrade", "status": "passed",
            "evidence_code": "upgrade-reviewed-private-migration",
        }
        self.assertTrue(harness.scanned_report_bytes(valid_report([scenario])))

    def test_version_selection_gates_write_only_features(self):
        self.assertFalse(harness.supports_optional_111("Terraform v1.1.0"))
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
        schema_pins = tools["previous_provider"]["schema_sha256_by_cli"]
        self.assertEqual(set(schema_pins), {
            "terraform-1.1.0", "terraform-1.11.4", "opentofu-1.6.3", "opentofu-1.11.1",
        })
        self.assertTrue(all(re.fullmatch(r"[0-9a-f]{64}", value) for value in schema_pins.values()))

    def test_report_is_strict_derived_exclusive_and_create_only(self):
        scenario = {
            "name": "inventory:provider-types", "subject": "provider-types",
            "category": "inventory", "status": "passed", "evidence_code": "inventory-validated",
        }
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw).resolve() / "report.json"
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

    def test_execution_artifacts_allow_safe_hmac_and_publish_report_last(self):
        receipt = "hmac-sha256:" + "1234" + "a" * 60
        ledger = (json.dumps({"receipt_hmac": receipt}, sort_keys=True) + "\n").encode()
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            report_path = root / "result.json"
            evidence_path = root / "result.evidence.jsonl"
            published = []
            def publish(path, encoded):
                published.append(path)
                path.write_bytes(encoded)
            with mock.patch.object(harness, "atomic_exclusive_write", side_effect=publish):
                harness.publish_execution_artifacts(report_path, evidence_path, valid_report(), ledger)
            self.assertEqual(published, [evidence_path, report_path])

    def test_unsafe_ledger_publishes_neither_evidence_nor_report(self):
        ledger = b'{"note":"https://private.invalid/value"}\n'
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            report_path = root / "result.json"
            evidence_path = root / "result.evidence.jsonl"
            with self.assertRaises(harness.HarnessError):
                harness.publish_execution_artifacts(report_path, evidence_path, valid_report(), ledger)
            self.assertFalse(report_path.exists())
            self.assertFalse(evidence_path.exists())

    def test_report_rejects_symlink_ancestor_and_survives_mid_swap(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            real = root / "real"
            real.mkdir(mode=0o700)
            ancestor = root / "ancestor"
            ancestor.symlink_to(real, target_is_directory=True)
            with self.assertRaises(harness.HarnessError):
                harness.write_report(ancestor / "report.json", valid_report())

        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            parent = root / "parent"
            parent.mkdir(mode=0o700)
            moved = root / "moved"
            victim = root / "victim"
            victim.mkdir(mode=0o700)
            original_link = os.link
            swapped = False
            def racing_link(src, dst, **kwargs):
                nonlocal swapped
                if not swapped:
                    parent.rename(moved)
                    parent.symlink_to(victim, target_is_directory=True)
                    swapped = True
                return original_link(src, dst, **kwargs)
            with mock.patch.object(harness.os, "link", side_effect=racing_link):
                harness.write_report(parent / "report.json", valid_report())
            self.assertTrue((moved / "report.json").is_file())
            self.assertFalse((victim / "report.json").exists())

    def test_refresh_completion_derives_all_35_not_only_28_plan_changes(self):
        matrix = harness.load_json(harness.MATRIX_PATH)
        types = matrix["data_sources"]
        configured = [
            {"mode": "data", "type": resource_type, "address": f"data.{resource_type}.all"}
            for resource_type in types
        ]
        configured.append({
            "mode": "data", "type": "litellm_credential",
            "address": "data.litellm_credential.full",
        })
        plan = {
            "configuration": {"root_module": {"resources": configured}},
            "resource_changes": [
                {"mode": "data", "type": resource_type, "change": {"actions": ["read"]}}
                for resource_type in types[:28]
            ],
        }
        raw_state = {
            "resources": [
                {"mode": "data", "type": resource_type, "name": "all", "instances": [{}]}
                for resource_type in types
            ] + [{"mode": "data", "type": "litellm_credential", "name": "full", "instances": [{}]}]
        }
        show_state = {
            "values": {"root_module": {"resources": [
                {"mode": "managed", "type": "litellm_model", "address": "litellm_model.test"},
                *[
                    {"mode": "data", "type": resource_type, "address": f"data.{resource_type}.all"}
                    for resource_type in types
                ] + [{"mode": "data", "type": "litellm_credential", "address": "data.litellm_credential.full"}],
            ]}}
        }
        steady = {"resource_changes": []}
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            paths = {
                "plan": root / "plan.json", "refresh_state": root / "refresh.tfstate",
                "state": root / "state.json", "steady_plan": root / "steady.json",
                "final_state": root / "final.list",
            }
            for name, value in (("plan", plan), ("refresh_state", raw_state), ("state", show_state), ("steady_plan", steady)):
                paths[name].write_text(json.dumps(value), encoding="utf-8")
            paths["final_state"].write_text("", encoding="utf-8")
            digest = harness._assertion_digest(
                [paths["plan"], paths["refresh_state"]], "refresh-only-config-state-zero-drift"
            )
            phase = {"record_type": "phase", "subjects": sorted(types), "assertion_sha256": digest}
            observed = []
            args = type("Args", (), {"session": str(root / "session.json"), "fallback_delete_unconfirmed": False, "fallback_delete_evidence": None, "fallback_presence_state": None, "fallback_presence_output": None, "fallback_delete_success_evidence": None, "fallback_delete_success_cli": None, "fallback_delete_success_argument": [], **{key: str(value) for key, value in paths.items()}})()
            with mock.patch.object(harness, "_read_session", return_value={}), \
                 mock.patch.object(harness, "_ledger_values", return_value=[phase]), \
                 mock.patch.object(harness, "_append_scenario", side_effect=lambda *a, **kw: observed.append(kw)):
                harness.observe_smoke(args)
            data_records = [item for item in observed if item["category"] == "data_source"]
            self.assertEqual(len(plan["resource_changes"]), 28)
            self.assertEqual({item["name"].split(":", 1)[1] for item in data_records}, set(types))
            self.assertTrue(all(item["command_record"] is phase for item in data_records))

    def test_fallback_smoke_diagnostic_is_bound_only_when_observed(self):
        plan = {"configuration": {"root_module": {"resources": []}}}
        raw_state = {"resources": []}
        show_state = {"values": {"root_module": {"resources": [
            {"mode": "managed", "type": "litellm_fallback", "address": "litellm_fallback.test"},
        ]}}}
        steady = {"resource_changes": []}
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            paths = {
                "plan": root / "plan.json", "refresh_state": root / "refresh.tfstate",
                "state": root / "state.json", "steady_plan": root / "steady.json",
                "final_state": root / "final.list",
            }
            for name, value in (("plan", plan), ("refresh_state", raw_state), ("state", show_state), ("steady_plan", steady)):
                paths[name].write_text(json.dumps(value), encoding="utf-8")
            paths["final_state"].write_text("", encoding="utf-8")
            for observed_failure, expected_code in ((False, ""), (True, "fallback-delete-not-authoritative")):
                observed = []
                args = type("Args", (), {
                    "session": str(root / "session.json"),
                    "fallback_delete_unconfirmed": observed_failure,
                    "fallback_delete_evidence": str(paths["final_state"]) if observed_failure else None,
                    "fallback_presence_state": str(paths["state"]) if observed_failure else None,
                    "fallback_presence_output": str(paths["final_state"]) if observed_failure else None,
                    "fallback_delete_success_evidence": None if observed_failure else str(paths["final_state"]),
                    "fallback_delete_success_cli": None if observed_failure else "terraform",
                    "fallback_delete_success_argument": [],
                    **{key: str(value) for key, value in paths.items()},
                })()
                presence_digest = harness._assertion_digest(
                    [paths["state"], paths["state"], paths["final_state"], paths["final_state"]], "terraform-plan-state-api"
                )
                phase = {"record_type": "phase", "phase": "fallback-authoritative-presence", "subjects": ["litellm_fallback"], "assertion_sha256": presence_digest}
                empty_result = hashlib.sha256(b"issue210-result-v1\0" + (0).to_bytes(4, "big", signed=True)).hexdigest()
                success = {"record_type": "command", "command_sha256": harness._command_digest(["terraform", "destroy", "-auto-approve"]), "result_sha256": empty_result, "exit_code": 0}
                with mock.patch.object(harness, "_read_session", return_value={}), \
                     mock.patch.object(harness, "_ledger_values", return_value=[phase] if observed_failure else [success]), \
                     mock.patch.object(harness, "_append_scenario", side_effect=lambda *a, **kw: observed.append(kw)):
                    harness.observe_smoke(args)
                lifecycle = next(item for item in observed if item["category"] == "lifecycle")
                self.assertEqual(lifecycle["status"], "skipped" if observed_failure else "passed")
                self.assertEqual(lifecycle["reason"], "fallback-delete-not-authoritative" if observed_failure else "")
                self.assertEqual(lifecycle["diagnostic_code"], expected_code)

    def test_refresh_phase_requires_exact_successful_refresh_only_argv(self):
        plan = {"configuration": {"root_module": {"resources": [
            {"mode": "data", "type": "litellm_models", "address": "data.litellm_models.all"},
            {"mode": "data", "type": "litellm_models", "address": "data.litellm_models.filtered"},
        ]}}}
        state = {"resources": [
            {"mode": "data", "type": "litellm_models", "name": "all", "instances": [{}]},
            {"mode": "data", "type": "litellm_models", "name": "filtered", "instances": [{}]},
        ]}
        bindings = {
            "run_nonce": "1" * 64, "cli_lane": "terraform-1.11.4",
            "candidate_commit": "2" * 40, "provider_sha256": "3" * 64,
            "provider_schema_sha256": "4" * 64, "harness_sha256": "5" * 64,
            "matrix_sha256": "6" * 64,
        }
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            plan_path, state_path = root / "plan.json", root / "state.json"
            plan_path.write_text(json.dumps(plan), encoding="utf-8")
            state_path.write_text(json.dumps(state), encoding="utf-8")
            session = {**bindings, "ledger": str(root / "ledger"), "key_file": str(root / "key")}
            args = type("Args", (), {
                "session": str(root / "session"), "plan": str(plan_path),
                "refresh_state": str(state_path), "cli": "/trusted/terraform",
                "refresh_argument": [],
            })()
            wrong = {"record_type": "command", **bindings, "exit_code": 0,
                     "command_sha256": "7" * 64, "result_sha256": "8" * 64}
            with mock.patch.object(harness, "_read_session", return_value=session), \
                 mock.patch.object(harness, "_ledger_values", return_value=[wrong]), \
                 self.assertRaisesRegex(harness.HarnessError, "exact supervised argv"):
                harness.capture_refresh_phase(args)
            exact = {**wrong, "command_sha256": harness._command_digest([
                "/trusted/terraform", "apply", "-refresh-only", "-auto-approve"
            ])}
            appended = []
            with mock.patch.object(harness, "_read_session", return_value=session), \
                 mock.patch.object(harness, "_ledger_values", return_value=[exact]), \
                 mock.patch.object(harness, "_session_key", return_value=b"k" * 32), \
                 mock.patch.object(harness, "_append_ledger", side_effect=lambda path, item: appended.append(item)):
                harness.capture_refresh_phase(args)
            self.assertEqual(appended[0]["command_sha256"], exact["command_sha256"])
            self.assertEqual(appended[0]["subjects"], ["litellm_models"])

    def test_phase_only_records_cannot_finalize(self):
        bindings = {
            "run_nonce": "1" * 64, "cli_lane": "terraform-1.11.4",
            "candidate_commit": "2" * 40, "provider_sha256": "3" * 64,
            "provider_schema_sha256": "4" * 64, "harness_sha256": "5" * 64,
            "matrix_sha256": "6" * 64,
        }
        command = {
            "record_type": "command", **bindings, "command_sha256": "7" * 64,
            "result_sha256": "8" * 64, "exit_code": 0, "output_bytes": 0,
            "receipt_hmac": "hmac-sha256:" + "9" * 64,
        }
        phase = {
            "record_type": "phase", **bindings, "phase": "refresh-only-data-sources",
            "subjects": ["litellm_model"], "command_sha256": "7" * 64,
            "result_sha256": "8" * 64, "command_exit_code": 0,
            "assertion_sha256": "a" * 64, "receipt_hmac": "hmac-sha256:" + "b" * 64,
        }
        args = type("Args", (), {"session": "unused", "cli": "unused", "previous_provider_binary": "unused", "report": "unused", "evidence_report": "unused"})()
        with mock.patch.object(harness, "_read_session", return_value=bindings), \
             mock.patch.object(harness, "_ledger_values", return_value=[{"record_type": "session"}, command, phase]), \
             self.assertRaisesRegex(harness.HarnessError, "exact scenario set"):
            harness.finalize_evidence(args)

    def test_digest_ledger_tampering_fails_signature_validation(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            key = root / "key"
            key.write_bytes(b"k" * 32)
            key.chmod(0o600)
            ledger = root / "ledger.jsonl"
            session = {
                "schema_version": harness.EVIDENCE_SCHEMA_VERSION,
                "run_nonce": "1" * 64, "cli_lane": "terraform-1.11.4",
                "candidate_commit": "2" * 40, "provider_sha256": "3" * 64,
                "provider_schema_sha256": "4" * 64, "harness_sha256": "5" * 64,
                "matrix_sha256": "6" * 64, "ledger": str(ledger), "key_file": str(key),
            }
            header = {"record_type": "session", **{name: value for name, value in session.items() if name not in {"ledger", "key_file"}}}
            header["receipt_hmac"] = harness._sign_record(header, b"k" * 32)
            ledger.write_text(json.dumps(header, sort_keys=True) + "\n", encoding="utf-8")
            ledger.chmod(0o600)
            self.assertEqual(harness._ledger_values(session)[0]["candidate_commit"], "2" * 40)
            header["candidate_commit"] = "7" * 40
            ledger.write_text(json.dumps(header, sort_keys=True) + "\n", encoding="utf-8")
            with self.assertRaises(harness.HarnessError):
                harness._ledger_values(session)

    def test_removed_manual_tsv_report_command_cannot_publish(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            records = root / "fabricated.tsv"
            records.write_text("upgrade:litellm_model\tupgrade\tpassed\t\t\n" * 166)
            report = root / "report.json"
            proc = subprocess.run(
                [sys.executable, str(harness.HERE / "harness.py"), "report-records",
                 "--records", str(records), "--report", str(report)],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False, timeout=30,
            )
            self.assertNotEqual(proc.returncode, 0)
            self.assertFalse(report.exists())

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

    def test_archive_duplicate_member_and_traversal_are_rejected(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            duplicate = root / "duplicate.zip"
            with zipfile.ZipFile(duplicate, "w") as package:
                package.writestr("terraform", b"first")
                package.writestr("terraform", b"second")
            with self.assertRaises(harness.HarnessError):
                harness.extract_executable(duplicate, root / "duplicate-out", "terraform", None)

            traversal = root / "traversal.zip"
            with zipfile.ZipFile(traversal, "w") as package:
                package.writestr("../decoy", b"escape")
                package.writestr("terraform", b"binary")
            with self.assertRaises(harness.HarnessError):
                harness.extract_executable(traversal, root / "traversal-out", "terraform", None)

    def test_command_deadline_and_output_bounds_fail_closed(self):
        deadline = harness.HERE / "deadline.py"
        timed = subprocess.run(
            [sys.executable, str(deadline), "--seconds", "1", sys.executable, "-c", "import time; time.sleep(3)"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15, check=False,
        )
        self.assertEqual(timed.returncode, 124)
        bounded = subprocess.run(
            [sys.executable, str(deadline), "--seconds", "10", "--max-output", "1024", sys.executable, "-c", "print('x'*2048)"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=15, check=False,
        )
        self.assertEqual(bounded.returncode, 125)
        self.assertLessEqual(len(bounded.stdout), 1024)

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

    def test_cache_paths_allow_trusted_macos_tmp_alias_but_reject_attacker_ancestor(self):
        tmp = Path("/tmp")
        canonical = harness.canonicalize_trusted_os_alias(tmp / "issue210-cache")
        if tmp.is_symlink():
            self.assertEqual(canonical, tmp.resolve() / "issue210-cache")
        else:
            self.assertEqual(canonical, tmp / "issue210-cache")
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            real = root / "real"
            real.mkdir(mode=0o700)
            attacker = root / "attacker"
            attacker.symlink_to(real, target_is_directory=True)
            with self.assertRaises(harness.HarnessError):
                harness.secure_directory(attacker / "cache")

    def test_session_key_is_unlinked_after_report_or_cleanup_failure(self):
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw).resolve()
            root.chmod(0o700)
            key = root / ".ledger-key-test"
            key.write_bytes(b"k" * 32)
            key.chmod(0o600)
            session_path = root / "session.json"
            session = {
                "schema_version": harness.EVIDENCE_SCHEMA_VERSION,
                "run_nonce": "1" * 64, "cli_lane": "terraform-1.11.4",
                "candidate_commit": "2" * 40, "provider_sha256": "3" * 64,
                "provider_schema_sha256": "4" * 64, "harness_sha256": "5" * 64,
                "matrix_sha256": "6" * 64, "ledger": str(root / "ledger.jsonl"),
                "key_file": str(key),
            }
            session_path.write_text(json.dumps(session), encoding="utf-8")
            session_path.chmod(0o600)
            harness.remove_session_key(type("Args", (), {"session": str(session_path)})())
            self.assertFalse(key.exists())
            self.assertTrue(session_path.exists())

    def test_concurrent_secure_cache_creation_is_race_safe(self):
        with tempfile.TemporaryDirectory() as raw:
            cache = Path(raw).resolve() / "shared" / "nested" / "cache"
            failures = []
            threads = [
                threading.Thread(target=lambda: failures.append(harness.secure_directory(cache)))
                for _ in range(12)
            ]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()
            self.assertEqual(failures, [cache] * 12)
            self.assertFalse(any(path.is_symlink() for path in (cache, cache.parent, cache.parent.parent)))

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
