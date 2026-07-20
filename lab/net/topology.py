#!/usr/bin/env python3
"""Containernet topology for the SPK join lab.

One LinuxBridge switch is the lab LAN segment. Attached to it:
  - tap-dsm   the xpenology QEMU VM's NIC (created by fabric-up.sh)
  - veth-cn   patch into the shared `kind` docker bridge, so the DSM VM,
              the kind nodes, and the lab registry share one L2 segment
  - d1        an alpine debug host for tcpdump/curl from inside the lab

LinuxBridge (not OVS) keeps this host's OVS untouched. Run fabric-up.sh
first; requires root. --cli drops into the mininet CLI, default runs
until SIGINT/SIGTERM.
"""

import argparse
import os
import signal
import sys
import time

RUN_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "run")

from mininet.net import Containernet
from mininet.nodelib import LinuxBridge
from mininet.link import Intf
from mininet.log import setLogLevel, info


def fabric_env():
    env = {}
    with open(os.path.join(RUN_DIR, "fabric.env")) as f:
        for line in f:
            k, _, v = line.strip().partition("=")
            env[k] = v
    return env


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--cli", action="store_true", help="interactive mininet CLI")
    parser.add_argument(
        "--debug-ip-suffix",
        default="200.2",
        help="last two octets of the debug host's IP inside the kind subnet",
    )
    args = parser.parse_args()

    if os.geteuid() != 0:
        sys.exit("run with sudo")

    env = fabric_env()
    subnet = env["SUBNET"]                      # e.g. 172.21.0.0/16
    gateway = env["GATEWAY"]
    prefix2 = ".".join(subnet.split(".")[:2])   # e.g. 172.21
    masklen = subnet.split("/")[1]
    debug_ip = f"{prefix2}.{args.debug_ip_suffix}/{masklen}"

    setLogLevel("info")
    net = Containernet(controller=None, switch=LinuxBridge)

    s1 = net.addSwitch("s1")

    # swlab-debug (lab/net/Dockerfile.debug, built by fabric-up.sh)
    # carries bash + iproute2: containernet drives Docker hosts through
    # bash and configures their interfaces with `ip`, and hangs or
    # silently leaves hosts unconfigured on images missing either.
    d1 = net.addDocker(
        "d1",
        ip=debug_ip,
        dimage="swlab-debug:latest",
        dcmd="sleep infinity",
    )
    net.addLink(d1, s1)

    net.start()
    # Enslave the pre-created host interfaces AFTER start: LinuxBridge
    # creates its kernel bridge in start(), so earlier Intf() attaches
    # do not stick.
    for ifname in ("tap-dsm", "veth-cn"):
        info(f"*** attaching {ifname} to s1\n")
        if os.system(f"ip link set {ifname} master s1 up") != 0:
            net.stop()
            sys.exit(f"failed to attach {ifname} to s1")
    # The debug host also has docker's own NAT interface; bring the lab
    # interface up explicitly and make the lab segment its default so
    # tests exercise the fabric, not docker0.
    d1.cmd("ip link set d1-eth0 up")
    d1.cmd(f"ip route replace default via {gateway} dev d1-eth0")
    info(f"*** lab fabric live: s1 = {{tap-dsm, veth-cn, d1@{debug_ip}}}\n")

    with open(os.path.join(RUN_DIR, "topology.pid"), "w") as f:
        f.write(str(os.getpid()))

    try:
        if args.cli:
            from mininet.cli import CLI

            CLI(net)
        else:
            stop = []
            signal.signal(signal.SIGTERM, lambda *a: stop.append(1))
            signal.signal(signal.SIGINT, lambda *a: stop.append(1))
            while not stop:
                time.sleep(1)
    finally:
        info("*** stopping topology\n")
        net.stop()
        try:
            os.remove(os.path.join(RUN_DIR, "topology.pid"))
        except FileNotFoundError:
            pass


if __name__ == "__main__":
    main()
