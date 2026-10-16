"""The DSM wizard's "Volume servers" step and how its answers become volume.servers."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
WIZARD = ROOT / 'diyspk/seaweedfs/src/wizard'
SETUP = ROOT / 'diyspk/seaweedfs/src/service-setup.sh'

LEGACY = '''kube:
  apiserver: https://api:6443
  tokenFile: /t
  namespace: ns
  masterService: seaweedfs-master
volume:
  dir: /volume1/seaweed
  ip: 192.0.2.10
  port: 8080
  grpcPort: 18080
  rack: nas
  max: 2000
  instances: 1
  extraFlags: ["-compactionMBps=69", "-metricsPort=9325"]
'''

MOUNTS = ('/dev/md2 / ext4 rw 0 0\n'
          '/dev/mapper/cachedev_0 /volume1 btrfs rw 0 0\n'
          '/dev/mapper/cachedev_1 /volume2 ext4 rw 0 0\n'
          '/dev/mapper/cachedev_3 /volume10 ext4 rw 0 0\n'
          '/dev/mapper/cachedev_1 /volume2/@docker ext4 rw 0 0\n')


class Wizard(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        tmp = Path(cls.tmp.name)
        cls.out = tmp / 'wizards'
        subprocess.run(['sh', str(WIZARD / 'assemble-wizards.sh'), str(WIZARD), str(cls.out)], check=True)
        cls.bootstrap = tmp / 'bootstrap'
        subprocess.run(['go', 'build', '-o', str(cls.bootstrap), '.'], check=True,
                       cwd=ROOT / 'cmd/synology-volume-bootstrap', env={**os.environ, 'GOWORK': 'off'})
        cls.mounts = tmp / 'mounts'
        cls.mounts.write_text(MOUNTS)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def wizard(self, name, config, bootstrap):
        with tempfile.TemporaryDirectory() as directory:
            result = Path(directory) / 'wizard.json'
            subprocess.run(['sh', str(self.out / f'{name}_uifile.sh')], check=True, env={
                **os.environ, 'SYNOPKG_TEMP_LOGFILE': str(result),
                'SEAWEEDFS_WIZARD_CONFIG': str(config), 'SEAWEEDFS_WIZARD_BOOTSTRAP': str(bootstrap),
                'SEAWEEDFS_WIZARD_MOUNTS': str(self.mounts)})
            return json.loads(result.read_text())

    @staticmethod
    def fields(steps):
        step = steps[-1]
        assert step['step_title'] == 'Volume servers', step
        return {s['key']: s['defaultValue'] for item in step['items'] for s in item.get('subitems', [])}

    def test_upgrade_from_a_single_server_offers_every_volume(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'volume.yaml'
            config.write_text(LEGACY)
            # the package being upgraded predates --print-servers
            steps = self.wizard('upgrade', config, '/nonexistent')
        self.assertEqual(steps[0]['step_title'], 'Upgrade')
        self.assertEqual(self.fields(steps), {
            'wizard_server_volume1': True, 'wizard_server_volume1_dir': '/volume1/seaweed',
            'wizard_server_volume1_memory_mb': '3072',
            'wizard_server_volume2': False, 'wizard_server_volume2_dir': '/volume2/seaweed',
            'wizard_server_volume2_memory_mb': '1536',
            'wizard_server_volume10': False, 'wizard_server_volume10_dir': '/volume10/seaweed',
            'wizard_server_volume10_memory_mb': '1536'})

    def test_upgrade_reads_the_current_server_list(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'volume.yaml'
            config.write_text(LEGACY)
            self.apply(config, {'wizard_server_volume1': 'true', 'wizard_server_volume1_dir': '/volume1/seaweed',
                                'wizard_server_volume1_memory_mb': '4096',
                                'wizard_server_volume10': 'true', 'wizard_server_volume10_dir': '/volume10/sw',
                                'wizard_server_volume10_memory_mb': '2048'})
            fields = self.fields(self.wizard('upgrade', config, self.bootstrap))
        self.assertEqual(fields['wizard_server_volume1_memory_mb'], '4096')
        self.assertTrue(fields['wizard_server_volume10'])
        self.assertEqual(fields['wizard_server_volume10_dir'], '/volume10/sw')
        self.assertFalse(fields['wizard_server_volume2'])

    def test_fresh_install_appends_the_step_to_the_install_wizard(self):
        steps = self.wizard('install', '/nonexistent', '/nonexistent')
        self.assertEqual([s['step_title'] for s in steps],
                         ['Kubernetes connection', 'Volume server settings', 'Volume servers'])
        fields = self.fields(steps)
        self.assertFalse(any(v for k, v in fields.items() if k.count('_') == 2), fields)
        self.assertEqual(fields['wizard_server_volume1_dir'], '/volume1/seaweedfs')

    def apply(self, config, answers):
        with tempfile.TemporaryDirectory() as directory:
            dest = Path(directory)
            (dest / 'bin').mkdir()
            (dest / 'bin/synology-volume-bootstrap').symlink_to(self.bootstrap)
            env = {k: v for k, v in os.environ.items() if not k.startswith('wizard_')}
            return subprocess.run(['sh', '-ec', '. "$SETUP"; VOLUME_YAML="$CONFIG"; apply_wizard_servers'], env={
                **env, **answers, 'SETUP': str(SETUP), 'CONFIG': str(config), 'SYNOPKG_PKGDEST': str(dest),
                'SYNOPKG_PKGVAR': directory}, text=True, capture_output=True)

    def servers(self, config):
        return subprocess.run([str(self.bootstrap), '--config', str(config), '--print-servers'],
                              check=True, text=True, capture_output=True).stdout.splitlines()

    def test_answers_become_the_server_list_and_keep_the_existing_identity(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'volume.yaml'
            config.write_text(LEGACY)
            result = self.apply(config, {
                'wizard_server_volume1': 'true', 'wizard_server_volume1_dir': '/volume1/seaweed',
                'wizard_server_volume1_memory_mb': '3072',
                'wizard_server_volume2': 'true', 'wizard_server_volume2_dir': '/volume2/seaweed',
                'wizard_server_volume2_memory_mb': '1536',
                'wizard_server_volume10': 'false', 'wizard_server_volume10_dir': '/volume10/seaweed'})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.servers(config), ['/volume1/seaweed\t8080\t18080\t9325\t3072',
                                                    '/volume2/seaweed\t8081\t18081\t9326\t1536'])

    def test_nothing_checked_leaves_the_configuration_alone(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'volume.yaml'
            config.write_text(LEGACY)
            result = self.apply(config, {'wizard_server_volume1': 'false'})
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(config.read_text(), LEGACY)

    def test_a_directory_off_its_volume_is_refused(self):
        with tempfile.TemporaryDirectory() as directory:
            config = Path(directory) / 'volume.yaml'
            config.write_text(LEGACY)
            for bad in ('/volume1/seaweed', '/volume2/a,b', '/volume2/a=1'):
                result = self.apply(config, {'wizard_server_volume2': 'true', 'wizard_server_volume2_dir': bad})
                self.assertNotEqual(result.returncode, 0, bad)
            self.assertEqual(config.read_text(), LEGACY)


if __name__ == '__main__':
    unittest.main()
