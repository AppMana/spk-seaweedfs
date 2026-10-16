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

    def run_with_servers(self, dirs, limit, existing=(), rejected=False):
        """Register a package whose bootstrap reports these server dirs."""
        script = Path(__file__).resolve().parents[1] / 'diyspk/seaweedfs/src/register-service.sh'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pkg, units, fs = root / 'pkg', root / 'units', root / 'fs'
            (pkg / 'target/bin').mkdir(parents=True)
            (pkg / 'var').mkdir()
            (pkg / 'var/volume.yaml').write_text('volume: {}\n')
            units.mkdir()
            for volume in ('volume1', 'volume2'):
                (fs / volume).mkdir(parents=True)
            for d in existing:
                (fs / d.lstrip('/')).mkdir(parents=True)
            bootstrap = pkg / 'target/bin/synology-volume-bootstrap'
            bootstrap.write_text('#!/bin/sh\n'
                                 '[ -z "$REJECT" ] || [ "$3" != --print-servers ] || { echo "invalid config" >&2; exit 1; }\n'
                                 'case "$3" in\n'
                                 '--print-servers) printf "%b" "$LISTING" ;;\n'
                                 '--print-memory-limit-mb) echo "$LIMIT" ;;\n'
                                 'esac\n')
            bootstrap.chmod(0o755)
            trace = root / 'commands'
            # Stub the DSM boundary only; directory creation is recorded and
            # performed without the ownership change, which needs root.
            stubs = (
                'id() { echo 0; }\n'
                'test() { return 0; }\n'
                'cmp() { return 0; }\n'
                # like grep, read stdin only without a file operand
                'grep() { for last; do :; done; case "$last" in /*) return 0 ;; esac; cat >/dev/null; }\n'
                'install() { printf "install %s\\n" "$*" >> "$TRACE"; for last; do :; done; mkdir -p "$last"; }\n'
                'systemctl() {\n'
                '    printf "systemctl %s\\n" "$*" >> "$TRACE"\n'
                '    if [ "$1" = show ]; then echo "Requires=pkg-seaweedfs-volume.service"; fi\n'
                '}\n'
                'sync() { echo sync >> "$TRACE"; }\n'
                '. "$SCRIPT"\n')
            listing = ''.join(f'{d}\\t8080\\t18080\\t9325\\t1536\\n' for d in dirs)
            result = subprocess.run(['sh', '-ec', stubs], env={
                **os.environ, 'TRACE': str(trace), 'SCRIPT': str(script),
                'SEAWEEDFS_PKG_DIR': str(pkg), 'SEAWEEDFS_UNIT_DIR': str(units),
                'SEAWEEDFS_FS_ROOT': str(fs), 'LISTING': listing, 'LIMIT': str(limit),
                'REJECT': '1' if rejected else ''},
                input='', text=True, capture_output=True)
            dropin = units / 'pkg-seaweedfs-volume.service.d/memory.conf'
            created = sorted(str(p.relative_to(fs)) for p in fs.glob('volume*/*'))
            return (result, trace.read_text().splitlines() if trace.exists() else [],
                    dropin.read_text() if dropin.exists() else None, created)

    def test_servers_get_their_directories_and_the_summed_memory_limit(self):
        result, commands, dropin, created = self.run_with_servers(
            ['/volume1/seaweed', '/volume2/seaweed'], 7168, existing=['/volume1/seaweed'])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(created, ['volume1/seaweed', 'volume2/seaweed'])
        creations = [c for c in commands if 'sc-seaweedfs' in c]
        self.assertEqual(len(creations), 1, commands)
        self.assertTrue(creations[0].endswith('fs/volume2/seaweed'), creations)
        self.assertEqual(dropin, '[Service]\nMemoryLimit=7168M\n')
        self.assertLess(commands.index('systemctl daemon-reload'),
                        commands.index('systemctl enable pkg-seaweedfs-volume.service'))
        self.assertIn('DSM_SERVICE_REGISTERED:', result.stdout)

    def test_single_server_keeps_the_unit_limit(self):
        result, commands, dropin, created = self.run_with_servers(['/volume1/seaweed'], 0)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIsNone(dropin)

    def test_directory_outside_a_dsm_volume_is_refused(self):
        result, commands, dropin, created = self.run_with_servers(['/etc/seaweed'], 3072)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('not under a DSM volume', result.stderr)
        self.assertNotIn('DSM_SERVICE_REGISTERED:', result.stdout)
        self.assertIsNone(dropin)

    def test_a_configuration_the_bootstrap_rejects_stops_registration(self):
        result, commands, dropin, created = self.run_with_servers(['/volume1/seaweed'], 3072, rejected=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn('DSM_SERVICE_REGISTERED:', result.stdout)
        self.assertIsNone(dropin)

    def test_directory_on_a_missing_volume_is_refused(self):
        result, commands, dropin, created = self.run_with_servers(['/volume7/seaweed'], 3072)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('is not a directory', result.stderr)
        self.assertEqual(created, [])

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
