import io
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
                   ('bin/run.sh', b'#!/bin/sh\n', 0o755), ('var/volume_template.yaml', b'volume: {}', 0o644)]
        hooks = [('scripts/' + name, b'#!/bin/sh\n', 0o755) for name in
                 ('preinst', 'postinst', 'preuninst', 'postuninst', 'preupgrade', 'postupgrade', 'start-stop-status')]
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'package.spk'
            def write(files):
                path.write_bytes(archive([('INFO', b'package="seaweedfs"\nversion="4.47-5"\narch="broadwellnk"\n', 0o644),
                                          ('package.tgz', archive(files, True), 0o644)] + hooks))
            write(payload)
            self.assertEqual(inspect_spk(path, '4.47-5')['version'], '4.47-5')
            with self.assertRaises(ValueError): inspect_spk(path, '4.40-4')
            for files in [payload[1:], payload + [payload[0]],
                          [('bin/weed', elf, 0o644)] + payload[1:],
                          payload + [('../escape', b'x', 0o644)]]:
                write(files)
                with self.assertRaises(ValueError): inspect_spk(path, '4.47-5')


if __name__ == '__main__':
    unittest.main()
