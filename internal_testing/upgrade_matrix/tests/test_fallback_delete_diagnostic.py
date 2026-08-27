import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "fallback_delete_diagnostic.py"
SPEC = importlib.util.spec_from_file_location("fallback_delete_diagnostic", MODULE_PATH)
module = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(module)


class FallbackDeleteDiagnosticTests(unittest.TestCase):
    def validate(self, text: str) -> None:
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "diagnostic"
            path.write_text(text, encoding="utf-8")
            module.validate(path)

    def reject(self, text: str) -> None:
        with self.assertRaises(ValueError):
            self.validate(text)

    def reviewed(self) -> str:
        return (
            "Warning: reviewed warning\nError: Fallback Delete Unconfirmed\n"
            "LiteLLM's authoritative fallback GET remained present after the bounded DELETE confirmation. "
            "Terraform state was retained.\n"
        )

    def test_accepts_only_exact_single_confirmation_error(self):
        self.validate(self.reviewed())
        self.reject("Error: unrelated not found\n")
        self.reject(self.reviewed() + "Error: unrelated\n")
        self.reject(self.reviewed() + "Error: Fallback Delete Unconfirmed\n")

    def test_rejects_operational_delete_failures_and_deadlines(self):
        self.reject(
            "Error: Fallback Delete Unconfirmed\n"
            "LiteLLM could not delete the fallback. Verify that the proxy is reachable.\n"
        )
        self.reject(
            "Error: Fallback Delete Unconfirmed\n"
            "LiteLLM returned HTTP status 500 while attempting to delete the fallback.\n"
        )
        self.reject(self.reviewed() + "command exceeded controlled deadline\n")
        self.reject(self.reviewed() + "request timed out while confirming fallback presence\n")
        self.reject(self.reviewed() + "operation was cancelled while confirming fallback presence\n")
        self.reject(self.reviewed() + "context deadline exceeded during confirmation\n")


if __name__ == "__main__":
    unittest.main()
