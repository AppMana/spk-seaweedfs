"""DSM guest wrapper: private disks, no implicit management NIC or ports."""
import argparse
import logging
import os
from pathlib import Path

import vrnetlab
from interfaces import declared_nics, isolate_control_listeners, wait_for_interfaces


class DSM(vrnetlab.VM):
    def __init__(self, nics):
        if nics != 1 or not Path('/private/private-image-marker').is_file():
            raise ValueError('DSM requires one declared NIC and a prepared private disk directory')
        super().__init__('', '', disk_image='/private/rr.img', ram=4096,
                         smp='4', provision_pci_bus=False)
        self.num_nics = nics
        self.nic_type = 'virtio-net-pci'
        self.conn_mode = 'tc'
        self.qemu_args[self.qemu_args.index('-machine') + 1] = 'q35'
        self.qemu_args.extend(['-drive', 'file=/private/data.qcow2,format=qcow2,if=ide'])
        isolate_control_listeners(self)

    def gen_mgmt(self):
        return ['-nic', 'none']

    def nic_provision_delay(self):
        wait_for_interfaces(self)

    def bootstrap_spin(self):
        # Wrapper health does not attest DSM readiness. The test authenticates
        # to the actual guest and checks its DSM identity and readiness marker.
        _, _, data = self.tn.expect([b'login:'], 1)
        if data:
            with open('/dsm-console.log', 'ab') as output:
                output.write(data)
        self.running = True

    def work(self):
        # The generic wrapper stops consuming serial output once running is
        # true. Keep collecting it throughout DSM boot for failure evidence.
        self.check_qemu()
        self.bootstrap_spin()


if __name__ == '__main__':
    os.environ['VR_MGMT_IS_A_LINK'] = 'true'
    parser = argparse.ArgumentParser()
    parser.add_argument('--hostname')
    # Containerlab supplies these generic VM flags; DSM uses the private
    # account injected into its copy, never the generic runtime credentials.
    parser.add_argument('--username')
    parser.add_argument('--password')
    parser.add_argument('--trace', action='store_true')
    parser.add_argument('--connection-mode', default='tc')
    parser.add_argument('--nics', type=int)
    args = parser.parse_args()
    if args.connection_mode != 'tc':
        raise ValueError('only the declared isolated tc dataplane is supported')
    logging.basicConfig(level=logging.INFO)
    runtime = vrnetlab.VR('', '')
    runtime.vms = [DSM(declared_nics(args.nics))]
    runtime.start()
