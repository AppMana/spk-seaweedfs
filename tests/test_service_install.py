"""Execute the real package hook with DSM7's post-sync directory layout."""
import os
from pathlib import Path
import pwd
import subprocess
import tempfile
import unittest


class ServiceInstall(unittest.TestCase):
    def test_reinstall_preserves_retained_config_and_token(self):
        root = Path(__file__).resolve().parents[1]
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            target, var = work / 'target', work / 'var'
            target.mkdir()
            var.mkdir()
            (var / 'kube').mkdir()
            (var / 'volume_template.yaml').write_bytes(
                (root / 'diyspk/seaweedfs/src/volume_template.yaml').read_bytes())
            config = b'volume:\n  dir: /volume1/retained-live-data\n  max: 17\n'
            token = b'retained-lab-only-token'
            (var / 'volume.yaml').write_bytes(config)
            (var / 'kube/token').write_bytes(token)
            result = subprocess.run(['sh', '-ec', '. "$HOOK"; service_postinst'],
                env={**os.environ, 'HOOK': str(root / 'diyspk/seaweedfs/src/service-setup.sh'),
                     'SYNOPKG_PKGNAME': 'seaweedfs', 'SYNOPKG_PKGDEST': str(target),
                     'SYNOPKG_PKGVAR': str(var), 'SYNOPKG_PKG_STATUS': 'INSTALL',
                     'SC_USER': pwd.getpwuid(os.getuid()).pw_name,
                     'wizard_token': 'replacement-must-not-be-used',
                     'wizard_advertise_ip': '192.0.2.20', 'wizard_rack': 'lab'},
                capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((var / 'volume.yaml').read_bytes(), config)
            self.assertEqual((var / 'kube/token').read_bytes(), token)

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
            # The lowest-numbered DSM volume gets the default server, in an
            # @ directory that DSM never manages as a shared folder.
            mounts = work / 'mounts'
            mounts.write_text('/dev/md0 / ext4 rw 0 0\n'
                              '/dev/mapper/cachedev_1 /volume10 ext4 rw 0 0\n'
                              '/dev/mapper/cachedev_0 /volume2 btrfs rw 0 0\n'
                              '/dev/mapper/cachedev_0 /volume2/@docker btrfs rw 0 0\n')
            result = subprocess.run(['sh', '-ec', '''
. "$HOOK"
service_postinst
'''], env={**os.environ, 'HOOK': str(root / 'diyspk/seaweedfs/src/service-setup.sh'),
           'SYNOPKG_PKGNAME': 'seaweedfs', 'SYNOPKG_PKGDEST': str(target),
           'SYNOPKG_PKGVAR': str(var), 'SYNOPKG_PKG_STATUS': 'INSTALL',
           'SC_USER': pwd.getpwuid(os.getuid()).pw_name, 'wizard_token': 'lab-only-token',
           'wizard_apiserver': 'https://192.0.2.10:6443', 'wizard_namespace': 'seaweedfs',
           'wizard_seaweed_name': 'qualification', 'wizard_advertise_ip': '192.0.2.20',
           'wizard_rack': 'synology', 'wizard_max_volumes': '8',
           'SEAWEEDFS_MOUNTS': str(mounts)},
                capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            config = (var / 'volume.yaml').read_text()
            self.assertIn('https://192.0.2.10:6443', config)
            self.assertNotIn('@PKGVAR@', config)
            self.assertIn('\n  dir: /volume2/@seaweedfs\n', config)
            self.assertNotIn('@SHARE_PATH@', config)
            self.assertEqual((var / 'kube/token').read_text(), 'lab-only-token')
            self.assertEqual((var / 'kube/token').stat().st_mode & 0o777, 0o600)
            self.assertEqual((var / 'volume.yaml').stat().st_mode & 0o777, 0o600)
            for name in ('volume.yaml', 'kube/token', 'log', 'log/weed.log'):
                self.assertEqual((var / name).stat().st_uid, os.getuid(), name)

    def test_package_requests_no_dsm_shared_folder(self):
        root = Path(__file__).resolve().parents[1]
        makefile = (root / 'diyspk/seaweedfs/Makefile').read_text()
        self.assertNotIn('SERVICE_WIZARD_SHARENAME', makefile)
        steps = (root / 'diyspk/seaweedfs/src/wizard/install-steps.json').read_text()
        self.assertNotIn('wizard_data_share', steps)
