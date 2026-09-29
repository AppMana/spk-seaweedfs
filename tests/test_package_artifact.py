import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from package_artifact import inspect_spk


def archive(files, compressed=False):
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode='w:gz' if compressed else 'w') as out:
        for name, data, mode in files:
            entry = tarfile.TarInfo(name)
            entry.size, entry.mode = len(data), mode
            out.addfile(entry, io.BytesIO(data))
    return stream.getvalue()


class PackageArtifactContract(unittest.TestCase):
    def test_rejects_incomplete_wrong_version_and_nonexecutables(self):
        elf = b'\x7fELF\x02\x01' + b'\0' * 12 + b'\x3e\0'
        payload = [('bin/weed', elf, 0o755), ('bin/synology-volume-bootstrap', elf, 0o755),
                   ('bin/run.sh', b'#!/bin/sh\n', 0o755), ('var/volume_template.yaml', b'volume: {}', 0o644),
                   ('bin/register-service.sh', b'#!/bin/sh\n', 0o755)]
        hooks = [('scripts/' + name, b'#!/bin/sh\n', 0o755) for name in
                 ('preinst', 'postinst', 'preuninst', 'postuninst', 'preupgrade', 'postupgrade', 'start-stop-status', 'volume-control')]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'package.spk'
            privilege = {'defaults': {'run-as': 'package'}}
            unit = b'''[Unit]
Before=pkgctl-seaweedfs.service
Requires=pkg-volume.target
After=pkg-volume.target
[Install]
RequiredBy=pkgctl-seaweedfs.service
[Service]
User=sc-seaweedfs
Group=synocommunity
Slice=seaweedfs.slice
LimitNOFILE=65536
LimitNPROC=4096
MemoryAccounting=true
MemoryLimit=5G
KillMode=control-group
ExecStart=/var/packages/seaweedfs/scripts/volume-control start
ExecStop=/var/packages/seaweedfs/scripts/volume-control stop
'''
            def write(files, privileges=privilege, service_unit=unit):
                path.write_bytes(archive([('INFO', b'package="seaweedfs"\nversion="4.47-5"\narch="broadwellnk"\n', 0o644),
                                          ('conf/privilege', json.dumps(privileges).encode(), 0o644),
                                          ('conf/systemd/pkg-seaweedfs-volume.service', service_unit, 0o644),
                                          ('package.tgz', archive(files, True), 0o644)] + hooks))
            write(payload)
            self.assertEqual(inspect_spk(path, '4.47-5')['version'], '4.47-5')
            with self.assertRaises(ValueError): inspect_spk(path, '4.40-4')
            for files in [payload[1:], payload[:-1], payload + [payload[0]],
                          [('bin/weed', elf, 0o644)] + payload[1:],
                          payload + [('../escape', b'x', 0o644)]]:
                write(files)
                with self.assertRaises(ValueError): inspect_spk(path, '4.47-5')

            for bad_unit in [unit.replace(b'Before=pkgctl-seaweedfs.service', b'Before=unrelated.service'),
                             unit.replace(b'Requires=pkg-volume.target', b'Requires=unrelated.target'),
                             unit.replace(b'After=pkg-volume.target', b'After=unrelated.target'),
                             unit.replace(b'RequiredBy=pkgctl-seaweedfs.service', b'RequiredBy=unrelated.service'),
                             unit.replace(b'User=sc-seaweedfs', b'User=root'),
                             unit.replace(b'MemoryLimit=5G', b'MemoryMax=5G'),
                             unit.replace(b'LimitNPROC=4096', b'TasksMax=4096'),
                             unit.replace(b'LimitNOFILE=65536', b'LimitNOFILE=4096')]:
                write(payload, service_unit=bad_unit)
                with self.assertRaises(ValueError): inspect_spk(path, '4.47-5')
            for privileges in [{'defaults': {'run-as': 'root'}},
                               {**privilege, 'ctrl-script': [{'action': 'postinst', 'run-as': 'root'}]},
                               {**privilege, 'ctrl-script': [{'action': 'postupgrade', 'run-as': 'root'}]},
                               {**privilege, 'ctrl-script': [{'action': 'start', 'run-as': 'root'}]}]:
                write(payload, privileges)
                with self.assertRaises(ValueError): inspect_spk(path, '4.47-5')


if __name__ == '__main__':
    unittest.main()
