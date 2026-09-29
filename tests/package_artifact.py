"""Inspect the exact expected SPK without executing or installing its payload."""
import argparse
import configparser
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile


def setting(path, key):
    values = re.findall(r'^' + re.escape(key) + r'\s*=\s*(\S+)\s*$', path.read_text(), re.M)
    if len(values) != 1:
        raise ValueError('expected one literal setting: ' + key)
    return values[0]


def members(archive):
    result = {}
    for member in archive.getmembers():
        path = PurePosixPath(member.name)
        if path.is_absolute() or '..' in path.parts:
            raise ValueError('unsafe archive path: ' + member.name)
        name = str(path)
        if name in result:
            raise ValueError('duplicate archive member: ' + name)
        result[name] = member
    return result


def contents(archive, entries, name):
    entry = entries.get(name)
    if entry is None or not entry.isfile() or entry.size > 512 * 1024**2:
        raise ValueError('missing, oversized or non-regular member: ' + name)
    return archive.extractfile(entry).read()


def inspect_spk(path, version):
    with tarfile.open(path) as outer:
        entries = members(outer)
        info = contents(outer, entries, 'INFO').decode()
        for key, expected in [('package', 'seaweedfs'), ('version', version)]:
            if re.findall(r'^' + key + r'="([^"]*)"$', info, re.M) != [expected]:
                raise ValueError('unexpected INFO ' + key)
        arch = re.findall(r'^arch="([^"]*)"$', info, re.M)
        if len(arch) != 1 or 'broadwellnk' not in arch[0].split():
            raise ValueError('expected x64 Synology architecture coverage')
        for script in ('preinst', 'postinst', 'preuninst', 'postuninst', 'preupgrade', 'postupgrade', 'start-stop-status', 'volume-control'):
            name = 'scripts/' + script
            contents(outer, entries, name)
            if not entries[name].mode & 0o111:
                raise ValueError('non-executable package hook: ' + name)
        privilege = json.loads(contents(outer, entries, 'conf/privilege'))
        if privilege.get('defaults', {}).get('run-as') != 'package':
            raise ValueError('daemon must retain unprivileged package identity')
        controls = privilege.get('ctrl-script', [])
        if any(item.get('run-as') != 'package' for item in controls):
            raise ValueError('DSM7 third-party package hooks must remain unprivileged')
        unit_data = contents(outer, entries, 'conf/systemd/pkg-seaweedfs-volume.service')
        unit = configparser.ConfigParser(interpolation=None, strict=False)
        unit.read_string(unit_data.decode())
        for section, key in [('Unit', 'Before'), ('Install', 'RequiredBy')]:
            if not unit.has_section(section) or unit[section].get(key) != 'pkgctl-seaweedfs.service':
                raise ValueError('missing bounded daemon dependency registration/order')
        for key in ('Requires', 'After'):
            if 'pkg-volume.target' not in unit['Unit'].get(key, '').split():
                raise ValueError('missing DSM package-volume boot dependency/order')
        required = {'User': 'sc-seaweedfs', 'Group': 'synocommunity', 'Slice': 'seaweedfs.slice',
                    'LimitNOFILE': '65536', 'LimitNPROC': '4096', 'MemoryAccounting': 'true',
                    'MemoryLimit': '5G', 'KillMode': 'control-group',
                    'ExecStart': '/var/packages/seaweedfs/scripts/volume-control start',
                    'ExecStop': '/var/packages/seaweedfs/scripts/volume-control stop'}
        if not unit.has_section('Service') or any(unit['Service'].get(key) != value for key, value in required.items()):
            raise ValueError('missing DSM-compatible unprivileged daemon resource limits')
        if any(key in unit['Service'] for key in ('MemoryMax', 'TasksMax')):
            raise ValueError('unsupported DSM219 resource property')
        control_hashes = {'conf/systemd/pkg-seaweedfs-volume.service': hashlib.sha256(unit_data).hexdigest()}
        for name in ('scripts/start-stop-status', 'scripts/volume-control'):
            control_hashes[name] = hashlib.sha256(contents(outer, entries, name)).hexdigest()
        payload = contents(outer, entries, 'package.tgz')
    hashes = {}
    with tarfile.open(fileobj=io.BytesIO(payload), mode='r:gz') as package:
        entries = members(package)
        for name in ('bin/weed', 'bin/synology-volume-bootstrap', 'bin/run.sh', 'bin/register-service.sh'):
            data = contents(package, entries, name)
            if not entries[name].mode & 0o111:
                raise ValueError('non-executable payload: ' + name)
            if not name.endswith('.sh'):
                if len(data) < 20 or data[:6] != b'\x7fELF\x02\x01' or data[18:20] != b'\x3e\x00':
                    raise ValueError('expected little-endian x86-64 ELF: ' + name)
            hashes[name] = hashlib.sha256(data).hexdigest()
        contents(package, entries, 'var/volume_template.yaml')
    return {'path': str(path), 'version': version,
            'sha256': hashlib.sha256(path.read_bytes()).hexdigest(), 'payload_sha256': hashes,
            'control_sha256': control_hashes}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tag', help='release tag must equal v<package-version>')
    parser.add_argument('--path-only', action='store_true')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    makefile = root / 'diyspk/seaweedfs/Makefile'
    version = setting(makefile, 'SPK_VERS') + '-' + setting(makefile, 'SPK_REV')
    if args.tag is not None and args.tag != 'v' + version:
        parser.error('tag does not match package version: v' + version)
    path = root / 'spksrc/packages' / ('seaweedfs_x64-7.2_' + version + '.spk')
    report = inspect_spk(path, version)
    print(path if args.path_only else json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
