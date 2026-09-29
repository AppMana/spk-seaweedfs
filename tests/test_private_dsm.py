"""Private-disk boundary tests; not a substitute for booting real DSM."""
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest


class PrivateDSMPreparation(unittest.TestCase):
    def test_preparation_never_writes_seeds_or_prints_password(self):
        script = Path(__file__).resolve().parents[1] / 'lab/dsm/prepare-private.sh'
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            rr, dsm = root / 'rr.img', root / 'data.qcow2'
            rr.write_bytes(b'boot seed')
            dsm.write_bytes(b'dsm seed')
            output = root / 'output'
            output.mkdir()
            commands = root / 'commands'
            commands.mkdir()
            # Model only the offline guestfish transport; execute the real
            # script, copy, hashing, credential generation and file permissions.
            sudo = commands / 'sudo'
            sudo.write_text('''#!/bin/sh
set -eu
case "$*" in *"-a $DSM_SEED "*) echo 'seed passed to guestfish' >&2; exit 99;; esac
case "$*" in
 *list-md-devices*) printf '/dev/md125\\n';;
 *vfs-type*) printf 'ext4\\n';;
 *is-file*) printf 'true\\n';;
 *upload*) printf '%s\\n' "$*" > "$OBSERVED_UPLOAD";;
 *) exit 98;;
esac
''')
            sudo.chmod(0o700)
            observed = root / 'upload'
            result = subprocess.run(['bash', str(script), str(rr), str(dsm), str(output)],
                                    env={**os.environ, 'PATH': str(commands) + ':' + os.environ['PATH'],
                                         'DSM_SEED': str(dsm), 'OBSERVED_UPLOAD': str(observed)},
                                    capture_output=True, text=True, check=True)
            prepared = list(output.iterdir())
            self.assertEqual(len(prepared), 1)
            private = prepared[0]
            self.assertEqual(rr.read_bytes(), b'boot seed')
            self.assertEqual(dsm.read_bytes(), b'dsm seed')
            self.assertNotEqual(dsm.stat().st_ino, (private / 'data.qcow2').stat().st_ino)
            self.assertEqual((private / 'data.qcow2').read_bytes(), b'dsm seed')
            credentials = dict(line.split('=', 1) for line in (private / 'account.env').read_text().splitlines())
            self.assertRegex(credentials['DSM_USER'], r'^swlab[0-9a-f]{12}$')
            self.assertRegex(credentials['DSM_PASS'], r'^[0-9a-f]{48}$')
            self.assertNotIn(credentials['DSM_PASS'], result.stdout + result.stderr)
            self.assertEqual(stat.S_IMODE((private / 'account.env').stat().st_mode), 0o600)
            self.assertEqual(stat.S_IMODE(private.stat().st_mode), 0o700)
            self.assertIn(str(private / 'data.qcow2'), observed.read_text())
            self.assertIn('/usr/rr/once.d/00-seaweedfs-lab-account.sh', observed.read_text())


if __name__ == '__main__':
    unittest.main()
