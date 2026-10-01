import json
import unittest
import subprocess
import sys
import tempfile
from pathlib import Path
from unittest.mock import patch
import bootstrap_suite
from bootstrap_suite import REQUIRED, verify


class BootstrapInventoryContract(unittest.TestCase):
    def test_go_stderr_is_retained_without_corrupting_json_inventory(self):
        events = [{'Action': 'pass', 'Test': name} for name in sorted(REQUIRED)]
        events.append({'Action': 'pass'})
        output = '\n'.join(json.dumps(row) for row in events)
        real_run = subprocess.run
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / 'tests.jsonl'
            def fake_go(*args, **kwargs):
                return real_run([sys.executable, '-c',
                                 'import sys; print(sys.argv[1]); print("go: downloading example/module v1.0.0", file=sys.stderr)',
                                 output], **kwargs)
            with patch.object(sys, 'argv', ['bootstrap_suite.py', '--log', str(log)]), \
                 patch.object(bootstrap_suite.subprocess, 'run', side_effect=fake_go):
                bootstrap_suite.main()
            verify(log.read_text(), 0)
            self.assertIn('go: downloading', Path(str(log) + '.stderr').read_text())

    def test_all_required_and_package_must_pass(self):
        events = [{'Action': 'pass', 'Test': name} for name in sorted(REQUIRED)]
        events.append({'Action': 'pass'})
        encode = lambda rows: '\n'.join(json.dumps(row) for row in rows)
        verify(encode(events), 0)
        for i in range(len(events)):
            with self.assertRaises(ValueError): verify(encode(events[:i] + events[i + 1:]), 0)
        for action in ('skip', 'fail'):
            with self.assertRaises(ValueError): verify(encode(events + [{'Action': action, 'Test': 'TestExtra/sub'}]), 0)
        with self.assertRaises(ValueError): verify(encode(events), 1)


if __name__ == '__main__':
    unittest.main()
