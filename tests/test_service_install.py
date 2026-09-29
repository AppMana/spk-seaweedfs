"""Execute the real package hook with DSM7's post-sync directory layout."""
import os
from pathlib import Path
import pwd
import subprocess
import tempfile
import unittest


class ServiceInstall(unittest.TestCase):
    def test_fresh_install_uses_persisted_template(self):
        root = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            target, var = work / 'target', work / 'var'
            target.mkdir()
            var.mkdir()
            # spksrc syno_sync_var_folder moves target/var into pkgvar
            # before invoking service_postinst on DSM7.
            (var / 'volume_template.yaml').write_bytes(
                (root / 'diyspk/seaweedfs/src/volume_template.yaml').read_bytes())
            result = subprocess.run(['sh', '-ec', '''
. "$HOOK"
# System unit writes need root and are covered by the real DSM test.
install_resource_limits() { :; }
service_postinst
'''], env={**os.environ, 'HOOK': str(root / 'diyspk/seaweedfs/src/service-setup.sh'),
           'SYNOPKG_PKGNAME': 'seaweedfs', 'SYNOPKG_PKGDEST': str(target),
           'SYNOPKG_PKGVAR': str(var), 'SYNOPKG_PKG_STATUS': 'INSTALL',
           'SC_USER': pwd.getpwuid(os.getuid()).pw_name, 'wizard_token': 'lab-only-token',
           'wizard_apiserver': 'https://192.0.2.10:6443', 'wizard_namespace': 'seaweedfs',
           'wizard_seaweed_name': 'qualification', 'wizard_advertise_ip': '192.0.2.20',
           'wizard_rack': 'synology', 'wizard_max_volumes': '8', 'SHARE_PATH': '/volume1/seaweed'},
                capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            config = (var / 'volume.yaml').read_text()
            self.assertIn('https://192.0.2.10:6443', config)
            self.assertNotIn('@PKGVAR@', config)
            self.assertEqual((var / 'kube/token').read_text(), 'lab-only-token')
            self.assertEqual((var / 'kube/token').stat().st_mode & 0o777, 0o600)
            self.assertEqual((var / 'volume.yaml').stat().st_mode & 0o777, 0o600)
            for name in ('volume.yaml', 'kube/token', 'log', 'log/weed.log'):
                self.assertEqual((var / name).stat().st_uid, os.getuid(), name)
