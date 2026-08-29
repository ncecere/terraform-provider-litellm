import importlib.util
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "import_convergence.py"
SPEC = importlib.util.spec_from_file_location("import_convergence", MODULE_PATH)
assert SPEC and SPEC.loader
convergence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(convergence)

TARGET = "litellm_key.minimal"
DEFAULT_OUTPUTS = {"matrix_import_id"}


def plan(target_actions, output_actions=None, unrelated_actions=None):
    changes = [{"address": TARGET, "change": {"actions": target_actions}}]
    if unrelated_actions is not None:
        changes.append(
            {"address": "litellm_team.other", "change": {"actions": unrelated_actions}}
        )
    outputs = {
        "key_minimal_id": {"actions": ["no-op"], "before": "redacted"}
    }
    if output_actions is not None:
        outputs["matrix_import_id"] = {
            "actions": output_actions,
            "before": "redacted",
        }
    return {"resource_changes": changes, "output_changes": outputs}


class ImportConvergenceTests(unittest.TestCase):
    def classify(self, value, allowed=DEFAULT_OUTPUTS):
        return convergence.classify_import_convergence(value, TARGET, allowed)

    def test_accepts_target_only_update(self):
        self.assertEqual(self.classify(plan(["update"])), "target-update")

    def test_accepts_terraform_11_stale_output_deletion(self):
        self.assertEqual(
            self.classify(plan(["no-op"], ["delete"])),
            "stale-output-delete",
        )

    def test_accepts_explicit_resource_specific_stale_output(self):
        value = plan(["no-op"], ["delete"])
        value["output_changes"]["credential_producer_id"] = {
            "actions": ["delete"]
        }
        self.assertEqual(
            self.classify(
                value, {"matrix_import_id", "credential_producer_id"}
            ),
            "stale-output-delete",
        )

    def test_target_update_may_delete_only_reviewed_stale_outputs(self):
        self.assertEqual(
            self.classify(plan(["update"], ["delete"])),
            "target-update",
        )

    def test_rejects_unrelated_resource_action(self):
        with self.assertRaises(convergence.ImportConvergenceError):
            self.classify(plan(["no-op"], ["delete"], ["update"]))

    def test_rejects_replacement_or_empty_detailed_plan(self):
        for value in (plan(["delete", "create"]), plan(["no-op"])):
            with self.subTest(value=value):
                with self.assertRaises(convergence.ImportConvergenceError):
                    self.classify(value)

    def test_rejects_unreviewed_or_non_delete_output_change(self):
        for name, actions in (
            ("other", ["delete"]),
            ("matrix_import_id", ["update"]),
        ):
            value = plan(["no-op"])
            value["output_changes"][name] = {"actions": actions}
            with self.subTest(name=name, actions=actions):
                with self.assertRaises(convergence.ImportConvergenceError):
                    self.classify(value)

    def test_rejects_missing_target(self):
        with self.assertRaises(convergence.ImportConvergenceError):
            self.classify({"resource_changes": [], "output_changes": {}})

    def test_stale_output_names_are_exact_and_bounded_by_grammar(self):
        self.assertEqual(
            convergence.parse_stale_outputs("credential_id,other_id"),
            {"matrix_import_id", "credential_id", "other_id"},
        )
        for value in ("bad-name", "name,../other", "1name"):
            with self.subTest(value=value):
                with self.assertRaises(convergence.ImportConvergenceError):
                    convergence.parse_stale_outputs(value)


if __name__ == "__main__":
    unittest.main()
