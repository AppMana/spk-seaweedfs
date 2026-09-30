from pathlib import Path
import unittest

from prepare_release import release_command, validate_identity


class DraftPromotion(unittest.TestCase):
    def test_identity_rejects_mismatch(self):
        valid = [Path('seaweedfs_x64-7.2_4.47-14.spk'), 'v4.47-14', 'a'*40, 'b'*64, 'b'*64]
        validate_identity(*valid)
        for index, value in [(0, Path('other.spk')), (1, 'v4.47-13'), (1, '--latest'),
                             (2, 'main'), (3, ''), (3, 'c'*64), (4, 'd'*64)]:
            args = valid.copy()
            args[index] = value
            with self.assertRaises(ValueError):
                validate_identity(*args)

    def test_only_draft_creation_no_rebuild_or_overwrite(self):
        args = release_command('v4.47-14', 'a'*40, [Path('/retained/package.spk')])
        self.assertEqual(args[:4], ['gh', 'release', 'create', 'v4.47-14'])
        self.assertIn('--draft', args)
        self.assertNotIn('--clobber', args)
        self.assertEqual(args[args.index('--target')+1], 'a'*40)
        self.assertEqual(args[-1], '/retained/package.spk')


if __name__ == '__main__':
    unittest.main()
