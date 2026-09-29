"""Exercise registration command ordering; real DSM power loss is the live gate."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class RegistrationDurability(unittest.TestCase):
    def run_registration(self, sync_status):
        script = Path(__file__).resolve().parents[1] / 'diyspk/seaweedfs/src/register-service.sh'
        with tempfile.TemporaryDirectory() as directory:
            trace = Path(directory) / 'commands'
            # Stub only the DSM boundary. Source the real registration script;
            # keep its set -e behavior and actual success marker unchanged.
            result = subprocess.run(['sh', '-ec', '''
id() { echo 0; }
test() { return 0; }
cmp() { return 0; }
grep() { cat >/dev/null; return 0; }
systemctl() {
    printf 'systemctl %s\n' "$*" >> "$TRACE"
    if [ "$1" = show ]; then echo 'Requires=pkg-seaweedfs-volume.service'; fi
}
sync() { echo sync >> "$TRACE"; return "$SYNC_STATUS"; }
. "$SCRIPT"
'''], env={**os.environ, 'TRACE': str(trace), 'SCRIPT': str(script),
           'SYNC_STATUS': str(sync_status)}, input='', text=True, capture_output=True)
            return result, trace.read_text().splitlines()

    def test_success_requires_durable_registration(self):
        result, commands = self.run_registration(0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(commands[-1], 'sync')
        self.assertIn('systemctl enable pkg-seaweedfs-volume.service', commands[:-1])
        self.assertIn('DSM_SERVICE_REGISTERED:', result.stdout)

    def test_failed_flush_never_reports_registered(self):
        result, commands = self.run_registration(75)
        self.assertEqual(result.returncode, 75, result.stderr)
        self.assertEqual(commands[-1], 'sync')
        self.assertNotIn('DSM_SERVICE_REGISTERED:', result.stdout)
