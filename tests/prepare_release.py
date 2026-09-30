"""Prepare a draft release from exact DSM-qualified bytes, without rebuilding."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import tempfile

from package_artifact import inspect_spk

REPOSITORY = 'AppMana/spk-seaweedfs'


def validate_identity(path, tag, source, qualified, actual):
    if not re.fullmatch(r'v[0-9]+\.[0-9]+-[0-9]+', tag):
        raise ValueError('expected v<package-version> tag')
    if not re.fullmatch(r'[0-9a-f]{40}', source):
        raise ValueError('full published package source revision required')
    if not re.fullmatch(r'[0-9a-f]{64}', qualified) or actual != qualified:
        raise ValueError('exact package has not been DSM-qualified')
    if path.name != 'seaweedfs_x64-7.2_' + tag[1:] + '.spk':
        raise ValueError('package filename/tag mismatch')


def release_command(tag, source, assets):
    return ['gh', 'release', 'create', tag, '--repo', REPOSITORY,
            '--draft', '--target', source, '--title', tag,
            '--notes', 'Exact package verified in the isolated DSM lifecycle lab. '
            'Draft only: no NAS installation or production rollout performed.',
            *map(str, assets)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--spk', required=True, type=Path)
    parser.add_argument('--tag', required=True)
    parser.add_argument('--source', required=True)
    parser.add_argument('--results-root', required=True, type=Path)
    args = parser.parse_args()
    qualified = subprocess.check_output([
        'gh', 'api', 'repos/' + REPOSITORY + '/actions/variables/DSM_QUALIFIED_SPK_SHA256',
        '--jq', '.value'], text=True).strip()
    with args.spk.open('rb') as stream:
        actual = hashlib.file_digest(stream, 'sha256').hexdigest()
    validate_identity(args.spk, args.tag, args.source, qualified, actual)
    report = inspect_spk(args.spk, args.tag[1:])
    if report['sha256'] != qualified:
        raise ValueError('package changed during inspection')
    # Verify the target is published, rather than silently targeting default main.
    observed = subprocess.check_output(['gh', 'api', 'repos/' + REPOSITORY + '/commits/' + args.source,
                                        '--jq', '.sha'], text=True).strip()
    if observed != args.source:
        raise ValueError('source revision is not published')
    args.results_root.mkdir(parents=True, exist_ok=True)
    output = Path(tempfile.mkdtemp(prefix='spk-draft.', dir=args.results_root))
    checksum = output / (args.spk.name + '.sha256')
    checksum.write_text(qualified + '  ' + args.spk.name + '\n')
    manifest = output / 'artifact-manifest.json'
    report.update(package_source=args.source, qualification='DSM lifecycle passed; draft not deployed')
    manifest.write_text(json.dumps(report, indent=2) + '\n')
    subprocess.run(release_command(args.tag, args.source, [args.spk, checksum, manifest]), check=True)
    # Read back the uploaded artifact; local inspection alone is not publication identity.
    download = output / 'readback'
    download.mkdir()
    subprocess.run(['gh', 'release', 'download', args.tag, '--repo', REPOSITORY,
                    '--pattern', args.spk.name, '--dir', str(download)], check=True)
    with (download / args.spk.name).open('rb') as stream:
        if hashlib.file_digest(stream, 'sha256').hexdigest() != qualified:
            raise ValueError('uploaded draft differs from qualified package')
    print('DRAFT_SPK_READBACK_VERIFIED ' + qualified + ' ' + str(output))


if __name__ == '__main__':
    main()
