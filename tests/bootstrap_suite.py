"""Require the reviewed bootstrap tests, not merely a successful go test exit."""
import argparse
import json
from pathlib import Path
import subprocess

REQUIRED = set('''
TestMaterializeWeed_NoImageNoOp TestMaterializeWeed_ExtractsBinary
TestMaterializeWeed_TopLayerWins TestMaterializeWeed_WhiteoutHides
TestMaterializeWeed_DigestPinMismatch TestMaterializeWeed_CacheHitOffline
TestMaterializeWeed_MultipleBinaries TestMaterializeWeed_MultiArchIndex
TestRenderArgs_BasicShape TestValidate_Defaults TestValidate_Required
TestInstanceConfig_Offsets TestInstanceConfig_SingleInstanceUnchanged
TestValidate_InstancesDefaultAndBounds TestValidate_MasterServiceSatisfiesRequirement
TestValidate_NeitherSeaweedNameNorMasterService TestDiscoverMasters_FromMasterServiceNoCRD
TestDiscoverMasters_HeadlessServiceUsesIPNotDNSName
TestDiscoverMasters_DefaultsToServiceDNSNotPodIPs TestDiscoverMasters_MasterServiceMissingEndpoints
TestDiscoverMasters_FromHeadlessFallback TestDiscoverMasters_FromService
TestDiscoverMasters_PortFallback TestMaterializeMTLS TestMaterializeMTLS_NoOpWhenSecretNameEmpty
'''.split())


def verify(output, code):
    events = [json.loads(line) for line in output.splitlines() if line.strip()]
    passed = {e.get('Test') for e in events if e.get('Action') == 'pass' and e.get('Test')}
    missing = REQUIRED - passed
    package_pass = any(e.get('Action') == 'pass' and not e.get('Test') for e in events)
    if code or missing or not package_pass or any(e.get('Action') in ('skip', 'fail') for e in events):
        raise ValueError('bootstrap suite incomplete or failed; missing=' + ','.join(sorted(missing)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--log', required=True, type=Path)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    with args.log.open('w') as output:
        result = subprocess.run(['go', 'test', '-json', '-race', '-count=1', './...'],
                                cwd=root / 'cmd/synology-volume-bootstrap',
                                stdout=output, stderr=subprocess.STDOUT, timeout=600)
    verify(args.log.read_text(), result.returncode)
    print('All %d required bootstrap tests passed: %s' % (len(REQUIRED), args.log))


if __name__ == '__main__':
    main()
