import json
import unittest
from bootstrap_suite import REQUIRED, verify


class BootstrapInventoryContract(unittest.TestCase):
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
