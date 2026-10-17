"""Regression checks use archived real task records, never invented tool output."""
import json
import copy
import hashlib
import struct
import zlib
import pathlib
import unittest

from qc_evidence import missing_tools, verify_case
from operator_context import require_operator_ui
from qc_png import decode_png
from qc_authorization import argument_failure, pending_failure, pending_identity
from qc_evidence import args, bound_records, release_claim_failures


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

    def test_reversed_release_claims_are_invalid_summary_input(self):
        case = self.cases[0]
        task = copy.deepcopy(case['task'])
        # Explicit corruption of an actual archived summary, never substituted
        # provider/tool output: exchange affected and fixed release labels.
        task['summary'] = task['summary'].replace('8.5', 'RELEASE_SWAP').replace('9.8', '8.5').replace('RELEASE_SWAP', '9.8')
        self.assertTrue(any('Summary release clauses' in failure for failure in verify_case('cited-search-original', task, case['records'])))

    def test_release_clause_punctuation_and_explicit_negation(self):
        summary = self.cases[0]['task']['summary']
        self.assertEqual([], release_claim_failures(summary))
        # Invalid derivatives preserve the real version/source material while
        # changing only the release-role assertion under review.
        negated = summary.replace('Fixed Release (Vendor Evidence)', 'Not Fixed Release (Vendor Evidence)')
        # A second explicit vendor-evidence statement remains in this archived
        # summary, so remove its release-role sentence rather than fabricating
        # replacement source evidence.
        negated = negated.split('Vendor Evidence Summary:')[0]
        self.assertTrue(release_claim_failures(negated))
        prefix = summary.split('Vendor Evidence Summary:')[0]
        self.assertEqual([], release_claim_failures(prefix.replace('9.8 / 9.8p1 (released July 1, 2024)', '9.8.')))

    def test_real_chromium_png_fully_decodes(self):
        raw = (pathlib.Path(__file__).parent / 'testdata/browser_research_mobile.png').read_bytes()
        self.assertEqual('1fc00cb5367cbea8df852009cac061ea31de54ea3d5b7cae0addfdee8e0eeaf2', hashlib.sha256(raw).hexdigest())
        result = decode_png(raw)
        self.assertEqual((375, 812), (result['width'], result['height']))
        self.assertEqual('9a410a9d68f88cd5baaed497f65b3dcb2d42297f116d8ec9853aa7ea12d173ac', result['pixel_sha256'])

    def test_real_png_truncated_and_corrupted_derivatives_rejected(self):
        raw = (pathlib.Path(__file__).parent / 'testdata/browser_research_mobile.png').read_bytes()
        for derivative in (raw[:33], raw[:-1], raw[:-12], raw + b'trailing corruption'):
            with self.subTest(length=len(derivative)), self.assertRaises(ValueError):
                decode_png(derivative)
        # Corrupt the real IDAT and separately repair its CRC. The first must
        # fail CRC; the second must fail actual zlib/pixel decoding.
        offset = 8
        while raw[offset + 4:offset + 8] != b'IDAT':
            offset += 12 + struct.unpack('>I', raw[offset:offset + 4])[0]
        length = struct.unpack('>I', raw[offset:offset + 4])[0]
        corrupted = bytearray(raw)
        corrupted[offset + 8] ^= 255
        with self.assertRaises(ValueError):
            decode_png(corrupted)
        crc = zlib.crc32(corrupted[offset + 4:offset + 8 + length]) & 0xffffffff
        corrupted[offset + 8 + length:offset + 12 + length] = struct.pack('>I', crc)
        with self.assertRaises(ValueError):
            decode_png(corrupted)

    def test_authorization_checks_actual_browser_call_prefix_and_refs(self):
        case = self.cases[2]
        task = case['task']
        history = bound_records(task, case['records'])
        for index, record in enumerate(history):
            proposed = args(record)
            if record['mcp_tool'] == 'browser_screenshot':
                # The archived fixed path is deliberately not re-authorized.
                self.assertTrue(argument_failure('browser-headless', record['mcp_tool'], proposed, task, history[:index]))
                continue
            self.assertEqual('', argument_failure('browser-headless', record['mcp_tool'], proposed, task, history[:index]), record['command'])
        click_index = next(i for i, r in enumerate(history) if args(r).get('action') == 'click')
        self.assertEqual('', argument_failure('browser-headless', 'browser_interact', {'action': 'click', 'ref': '@e2'}, task, history[:click_index]))
        self.assertTrue(argument_failure('browser-headless', 'browser_interact', {'action': 'click', 'ref': '@e99'}, task, history[:click_index]))

    def test_corrupted_authorization_arguments_do_not_expand_operator_scope(self):
        case = self.cases[2]
        task = case['task']
        history = bound_records(task, case['records'])
        actual_open = next(r for r in history if r['mcp_tool'] == 'browser_open')
        proposed = args(actual_open)
        proposed['url'] = 'https://www.openssh.com'
        self.assertTrue(argument_failure('browser-headless', 'browser_open', proposed, task, history[:1]))
        self.assertTrue(argument_failure('browser-headless', 'browser_tabs', {'action': 'new'}, task, history))
        self.assertTrue(argument_failure('browser-headless', 'browser_session', {'action': 'switch', 'session': 'other'}, task, history))

    def test_pending_authority_never_comes_from_display_command(self):
        case = self.cases[2]
        task = copy.deepcopy(case['task'])
        task['status'] = 'awaiting_approval'
        task['pending_command'] = 'browser click #submit'
        # Deliberately invalid pending-metadata input derived from the actual
        # task; no fictional provider or execution output is introduced.
        self.assertTrue(pending_failure('browser-headless', task, [], task['id'], task['session_id']))
        task['pending_proposal_id'] = case['records'][0]['id']
        task['pending_mcp_tool'] = 'browser_open'
        task['pending_mcp_args'] = '{"url":"https://outside.invalid","url":"http://127.0.0.1:18888/browser-qa.html"}'
        self.assertTrue(pending_failure('browser-headless', task, [], task['id'], task['session_id']))

    def test_command_metadata_refuses_threat_and_changed_proposal(self):
        case = self.cases[2]
        history = bound_records(case['task'], case['records'])
        index = next(i for i, record in enumerate(history) if args(record).get('action') == 'fill')
        record = history[index]
        # Construct pending authorization input from an unchanged real call.
        # These fields are an API-input test, not an archived pending event.
        task = copy.deepcopy(case['task'])
        task.update(status='awaiting_approval', pending_approval_kind='command',
                    pending_proposal_id=record['id'], pending_mcp_tool=record['mcp_tool'],
                    pending_mcp_args=record['mcp_args'])
        self.assertEqual('', pending_failure('browser-headless', task, history[:index], task['id'], task['session_id']))
        identity = pending_identity(task)
        task['pending_approval_kind'] = 'threat'
        self.assertTrue(pending_failure('browser-headless', task, history[:index], task['id'], task['session_id']))
        task['pending_approval_kind'] = 'command'
        task['pending_proposal_id'] = history[0]['id']
        self.assertIn('changed', pending_failure('browser-headless', task, history[:index], task['id'], task['session_id'], identity))
        task['pending_proposal_id'] = record['id']
        del task['pending_mcp_args']
        self.assertTrue(pending_failure('browser-headless', task, history[:index], task['id'], task['session_id']))

    def test_malformed_authorization_values_refused_without_type_errors(self):
        case = self.cases[0]
        search = next(record for record in case['records'] if record['mcp_tool'] == 'web_search')
        proposed = args(search)
        proposed['include_domains'] = [{}]
        self.assertTrue(argument_failure('cited-search-original', 'web_search', proposed, case['task'], []))
        self.assertTrue(argument_failure('cited-search-original', 'web_fetch', {'url': []}, case['task'], []))
        case = self.cases[2]
        self.assertTrue(argument_failure('browser-headless', 'browser_interact', {'action': 'click', 'selector': []}, case['task'], case['records']))

    def test_deep_task_rejects_schemeless_summary_citations(self):
        case = self.cases[1]
        failures = verify_case('deep-multi-source', case['task'], case['records'])
        self.assertEqual(2, sum('Exact HTTPS fetched source citation missing' in failure for failure in failures))
        self.assertIn('Fetched excerpts lack explicit affected-range/fixed-release evidence; fetch the original release notes', failures)

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
