"""The packaged daemon must run inside limits supported by DSM's systemd 219."""
import configparser
from pathlib import Path
import unittest


class ServiceUnit(unittest.TestCase):
    def test_native_daemon_waits_for_dsm_package_storage(self):
        # Real power-loss boot reproduced the daemon starting five seconds
        # before pkg-volume.target. Parent ordering is NOT inherited by a
        # RequiredBy child: both activation and ordering must be explicit.
        root = Path(__file__).resolve().parents[1]
        config = configparser.ConfigParser(interpolation=None, strict=False)
        config.read(root / 'diyspk/seaweedfs/src/pkg-seaweedfs-volume.service')
        self.assertIn('pkg-volume.target', config['Unit'].get('Requires', '').split())
        self.assertIn('pkg-volume.target', config['Unit'].get('After', '').split())

    def test_unprivileged_unit_has_aggregate_memory_and_uid_task_limits(self):
        root = Path(__file__).resolve().parents[1]
        unit = root / 'diyspk/seaweedfs/src/pkg-seaweedfs-volume.service'
        config = configparser.ConfigParser(interpolation=None, strict=False)
        config.read_string(unit.read_text())
        service = config['Service']
        self.assertEqual(config['Unit']['Before'], 'pkgctl-seaweedfs.service')
        self.assertEqual(config['Install']['RequiredBy'], 'pkgctl-seaweedfs.service')
        self.assertEqual(service['User'], 'sc-seaweedfs')
        self.assertEqual(service['Group'], 'synocommunity')
        self.assertEqual(service['Slice'], 'seaweedfs.slice')
        self.assertEqual(service['LimitNOFILE'], '65536')
        self.assertEqual(service['LimitNPROC'], '4096')
        self.assertEqual(service['MemoryAccounting'], 'true')
        self.assertEqual(service['MemoryLimit'], '5G')
        self.assertNotIn('MemoryMax', service)
        self.assertNotIn('TasksMax', service)
        self.assertEqual(service['KillMode'], 'control-group')
        self.assertEqual(service['ExecStart'], '/var/packages/seaweedfs/scripts/volume-control start')
        self.assertEqual(service['ExecStop'], '/var/packages/seaweedfs/scripts/volume-control stop')
