import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "agent_import_diagnostic.py"
SPEC = importlib.util.spec_from_file_location("agent_import_diagnostic", MODULE_PATH)
module = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(module)


class AgentImportDiagnosticTests(unittest.TestCase):
    def validate(self, text: str) -> None:
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "diagnostic"
            path.write_text(text, encoding="utf-8")
            module.validate(path)

    def reject(self, text: str) -> None:
        with self.assertRaises(ValueError):
            self.validate(text)

    def test_accepts_exact_wrapped_core_diagnostic(self):
        self.validate(
            "Error: Provider produced invalid plan\n\n"
            "Provider planned an invalid value for\n"
            "litellm_agent.minimal.agent_card.provider: planned for existence but config wants absence.\n"
        )

    def test_rejects_generic_or_additional_errors(self):
        self.reject("Error: Provider produced invalid plan\nnot the reviewed detail\n")
        self.reject(
            "Error: Provider produced invalid plan\n"
            "planned an invalid value for litellm_agent.minimal.agent_card.provider: "
            "planned for existence but config wants absence.\n"
            "Error: unrelated\n"
        )

    def test_rejects_wrong_path_or_direction(self):
        self.reject(
            "Error: Provider produced invalid plan\n"
            "planned an invalid value for litellm_agent.minimal.agent_card.capabilities: "
            "planned for existence but config wants absence.\n"
        )
        self.reject(
            "Error: Provider produced invalid plan\n"
            "planned an invalid value for litellm_agent.minimal.agent_card.provider: "
            "planned for absence but config wants existence.\n"
        )


if __name__ == "__main__":
    unittest.main()
