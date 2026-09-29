"""Containerlab argument contract; real DSM readiness requires the VM test."""
from pathlib import Path
import runpy
import sys
import types
import unittest
from unittest.mock import patch


class DSMWrapperContract(unittest.TestCase):
    def test_containerlab_arguments_and_isolated_disks(self):
        started = []

        class VM:
            def __init__(self, *args, **kwargs):
                self.inputs = kwargs
                self.qemu_args = ['-machine', 'pc']

        class VR:
            def __init__(self, *args):
                self.vms = []

            def start(self):
                started.extend(self.vms)

        interfaces = types.SimpleNamespace(
            declared_nics=lambda n: n or 1,
            isolate_control_listeners=lambda vm: None,
            wait_for_interfaces=lambda vm: None)
        wrapper = Path(__file__).resolve().parents[1] / 'lab/dsm/launch-dsm.py'
        argv = [str(wrapper), '--hostname', 'dsm', '--username', 'clab',
                '--password', 'generic-unused', '--trace', '--connection-mode', 'tc']
        with patch.dict(sys.modules, vrnetlab=types.SimpleNamespace(VM=VM, VR=VR),
                        interfaces=interfaces), patch.object(sys, 'argv', argv), \
                patch.object(Path, 'is_file', return_value=True):
            runpy.run_path(str(wrapper), run_name='__main__')
        self.assertEqual(len(started), 1)
        vm = started[0]
        self.assertEqual(vm.inputs['disk_image'], '/private/rr.img')
        self.assertEqual(vm.gen_mgmt(), ['-nic', 'none'])
        self.assertEqual(vm.num_nics, 1)
        self.assertIn('file=/private/data.qcow2,format=qcow2,if=ide', vm.qemu_args)
        self.assertEqual(vm.qemu_args[1], 'q35')
        calls = []
        vm.running = True
        vm.check_qemu = lambda: calls.append('health')
        vm.bootstrap_spin = lambda: calls.append('serial')
        vm.work()
        vm.work()
        self.assertEqual(calls, ['health', 'serial', 'health', 'serial'])
