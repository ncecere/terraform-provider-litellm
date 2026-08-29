import importlib.util
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "prompt_import_diagnostic.py"
SPEC = importlib.util.spec_from_file_location("prompt_import_diagnostic", MODULE_PATH)
assert SPEC and SPEC.loader
prompt_import_diagnostic = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(prompt_import_diagnostic)


class PromptImportDiagnosticTests(unittest.TestCase):
    def validate(self, text: str) -> None:
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "diagnostic.log"
            path.write_text(text, encoding="utf-8")
            prompt_import_diagnostic.validate(path)

    def test_accepts_exact_fail_closed_error(self):
        self.validate(
            "Error: Prompt Read Error\n\n"
            "Unable to read and validate the scoped prompt. Response and request details were omitted.\n"
        )

    def test_rejects_unrelated_or_multiple_errors(self):
        for text in (
            "Error: Cannot import non-existent remote object\n",
            "Error: Prompt Read Error\nError: Other Error\n"
            "Unable to read and validate the scoped prompt. Response and request details were omitted.\n",
            "Error: Prompt Read Error\nUnable to read prompt at https://example.invalid.\n",
        ):
            with self.subTest(text=text):
                with self.assertRaises(ValueError):
                    self.validate(text)

    def test_rejects_oversized_evidence(self):
        with tempfile.TemporaryDirectory() as raw:
            path = Path(raw) / "diagnostic.log"
            path.write_bytes(b"x" * (prompt_import_diagnostic.MAX_BYTES + 1))
            with self.assertRaises(ValueError):
                prompt_import_diagnostic.validate(path)


if __name__ == "__main__":
    unittest.main()
