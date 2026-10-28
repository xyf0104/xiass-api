import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class AuditReportGateTests(unittest.TestCase):
    def run_report(self, report):
        artifacts = ROOT.parent / "artifacts"
        artifacts.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="audit-gate-", dir=artifacts) as folder:
            audit = Path(folder) / "audit.json"
            exceptions = Path(folder) / "exceptions.yml"
            audit.write_text(json.dumps(report), encoding="utf-8")
            exceptions.write_text("version: 1\nexceptions:\n", encoding="utf-8")
            return subprocess.run(
                [sys.executable, str(ROOT / "tools/check_pnpm_audit_exceptions.py"),
                 "--audit", str(audit), "--exceptions", str(exceptions)],
                capture_output=True, text=True, check=False,
            )

    def test_clean_supported_reports_pass(self):
        for report in ({"advisories": {}}, {"vulnerabilities": {}}):
            with self.subTest(report=report):
                result = self.run_report(report)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_missing_or_failed_results_never_pass(self):
        for report in (
            {}, [], None, {"metadata": {"vulnerabilities": {"high": 0}}},
            {"advisories": []},
            {"error": {"code": "ERR_PNPM_AUDIT_BAD_RESPONSE"}},
            {"advisories": {}, "error": {"code": "TIMEOUT"}},
            {"vulnerabilities": {}, "errors": ["registry unavailable"]},
        ):
            with self.subTest(report=report):
                result = self.run_report(report)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("refusing to pass", result.stderr)

    def test_high_advisory_without_exception_still_fails(self):
        result = self.run_report({"advisories": {"fixture": {
            "module_name": "fixture-package", "severity": "high",
            "github_advisory_id": "GHSA-fixture", "title": "fixture advisory",
        }}})
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("High/Critical vulnerabilities missing exceptions", result.stdout + result.stderr)


if __name__ == "__main__":
    unittest.main()
