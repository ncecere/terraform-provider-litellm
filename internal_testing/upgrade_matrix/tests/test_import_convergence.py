import importlib.util
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "import_convergence.py"
SPEC = importlib.util.spec_from_file_location("import_convergence", MODULE_PATH)
assert SPEC and SPEC.loader
convergence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(convergence)


def plan(target_actions, output_actions=None, unrelated_actions=None):
    changes = [
        {"address": "litellm_key.minimal", "change": {"actions": target_actions}}
    ]
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
    def test_accepts_target_only_update(self):
        self.assertEqual(
            convergence.classify_import_convergence(
                plan(["update"]), "litellm_key.minimal"
            ),
            "target-update",
        )

    def test_accepts_terraform_11_stale_output_deletion(self):
        self.assertEqual(
            convergence.classify_import_convergence(
                plan(["no-op"], ["delete"]), "litellm_key.minimal"
            ),
            "stale-output-delete",
        )

    def test_target_update_may_delete_only_the_stale_output(self):
        self.assertEqual(
            convergence.classify_import_convergence(
                plan(["update"], ["delete"]), "litellm_key.minimal"
            ),
            "target-update",
        )

    def test_rejects_unrelated_resource_action(self):
        with self.assertRaises(convergence.ImportConvergenceError):
            convergence.classify_import_convergence(
                plan(["no-op"], ["delete"], ["update"]),
                "litellm_key.minimal",
            )

    def test_rejects_replacement_or_empty_detailed_plan(self):
        for value in (plan(["delete", "create"]), plan(["no-op"])):
            with self.subTest(value=value):
                with self.assertRaises(convergence.ImportConvergenceError):
                    convergence.classify_import_convergence(
                        value, "litellm_key.minimal"
                    )

    def test_rejects_unrelated_output_change(self):
        value = plan(["no-op"], ["delete"])
        value["output_changes"]["other"] = {"actions": ["update"]}
        with self.assertRaises(convergence.ImportConvergenceError):
            convergence.classify_import_convergence(value, "litellm_key.minimal")

    def test_rejects_missing_target(self):
        with self.assertRaises(convergence.ImportConvergenceError):
            convergence.classify_import_convergence(
                {"resource_changes": [], "output_changes": {}},
                "litellm_key.minimal",
            )


if __name__ == "__main__":
    unittest.main()
