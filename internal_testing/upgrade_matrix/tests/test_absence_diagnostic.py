import importlib.util
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "absence_diagnostic.py"
SPEC = importlib.util.spec_from_file_location("absence_diagnostic", MODULE_PATH)
assert SPEC and SPEC.loader
absence = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(absence)


class AuthoritativeAbsenceTests(unittest.TestCase):
    def test_accepts_reviewed_core_and_provider_absence(self):
        accepted = (
            ("Error: Cannot import non-existent remote object", "litellm_team"),
            ("Error: Remote object does not exist", "litellm_model"),
            (
                "Error: Unable to read team: API request failed with status 404; "
                "response detail omitted",
                "litellm_team",
            ),
            (
                "Unable to read budget: budget import read response did not contain "
                "exactly one budget",
                "litellm_budget",
            ),
            (
                "Error: Fallback Import Read Error LiteLLM returned HTTP status 404 "
                "while attempting to read during import the fallback.",
                "litellm_fallback",
            ),
            (
                "Error: Client Error\nUnable to read tag: API request failed with "
                "status 500: 404: Tags not found: ['test-tag-minimal']",
                "litellm_tag",
            ),
        )
        for diagnostic, resource_type in accepted:
            with self.subTest(diagnostic=diagnostic, resource_type=resource_type):
                self.assertTrue(
                    absence.is_authoritative_not_found(diagnostic, resource_type)
                )

    def test_rejects_unrelated_not_found_and_status_text(self):
        rejected = (
            "Error: required dependency plugin not found",
            "Error: provider package request failed with status code: 404",
            "Error: registry lookup failed with status=404",
            "Error: unrelated object was not found",
        )
        for diagnostic in rejected:
            with self.subTest(diagnostic=diagnostic):
                self.assertFalse(
                    absence.is_authoritative_not_found(diagnostic, "litellm_team")
                )
        self.assertFalse(
            absence.is_authoritative_not_found(
                "Unable to read tag: API request failed with status 500: "
                "dependency plugin not found",
                "litellm_tag",
            )
        )
        self.assertFalse(
            absence.is_authoritative_not_found(
                "Unable to read tag: API request failed with status 500: 404: "
                "Tags not found: []",
                "litellm_tag",
            )
        )

    def test_forbidden_or_malformed_evidence_fails_closed(self):
        self.assertFalse(
            absence.is_authoritative_not_found(
                "Invalid address; cannot import non-existent remote object",
                "litellm_team",
            )
        )
        self.assertFalse(
            absence.is_authoritative_not_found(
                "Cannot import non-existent remote object", "other_team"
            )
        )
        self.assertFalse(
            absence.is_authoritative_not_found(
                "Cannot import non-existent remote object"
                + ("x" * absence.MAX_EVIDENCE_BYTES),
                "litellm_team",
            )
        )


if __name__ == "__main__":
    unittest.main()
