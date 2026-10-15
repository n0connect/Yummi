"""Regression checks use archived real task records, never invented tool output."""
import json
import pathlib
import unittest

from qc_evidence import missing_tools, verify_case
from operator_context import require_operator_ui


class ArchivedEvidenceChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.cases = json.loads((pathlib.Path(__file__).parent / 'testdata/browser_research_history.json').read_text())

    def test_operator_ui_guard_rejects_ordinary_test_runtime(self):
        with self.assertRaises(RuntimeError):
            require_operator_ui()

    def test_cited_vendor_task_retains_exact_https_and_versions(self):
        case = self.cases[0]
        self.assertEqual([], missing_tools('cited-search-original', case['task'], case['records']))
        self.assertEqual([], verify_case('cited-search-original', case['task'], case['records']))

    def test_deep_task_rejects_schemeless_summary_citations(self):
        case = self.cases[1]
        failures = verify_case('deep-multi-source', case['task'], case['records'])
        self.assertEqual(2, len(failures))
        self.assertTrue(all('Exact HTTPS fetched source citation missing' in failure for failure in failures))

    def test_headless_flow_valid_but_legacy_artifact_unverified(self):
        case = self.cases[2]
        failures = verify_case('browser-headless', case['task'], case['records'])
        self.assertEqual(2, len(failures))
        self.assertTrue(all('artifact' in failure or 'screenshot hash' in failure for failure in failures))
        self.assertFalse(any('Ordered' in failure or 'mode' in failure for failure in failures))

    def test_full_browser_rejects_pre_click_snapshot_and_png(self):
        case = self.cases[3]
        self.assertTrue(any('Ordered fill' in failure for failure in verify_case('browser-full', case['task'], case['records'])))

    def test_required_tools_recomputed_from_actual_remaining_records(self):
        case = self.cases[0]
        # Evidence removal is not replacement output: every surviving record is
        # still an unchanged real execution from the archived task.
        without_fetches = [r for r in case['records'] if r['mcp_tool'] != 'web_fetch']
        self.assertEqual(['web_fetch'], missing_tools('cited-search-original', case['task'], without_fetches))
        self.assertIn('Actual original OpenSSH HTTPS fetch missing', verify_case('cited-search-original', case['task'], without_fetches))

    def test_other_real_session_cannot_supply_browser_evidence(self):
        case = self.cases[2]
        unrelated = self.cases[3]['records']
        self.assertTrue(missing_tools('browser-headless', case['task'], unrelated))
        self.assertIn('Executions outside the dedicated task/session lifetime', verify_case('browser-headless', case['task'], unrelated))


if __name__ == '__main__':
    unittest.main()
