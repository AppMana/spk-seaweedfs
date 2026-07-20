# xpenology DSM VM (lab)

QEMU/KVM VM booting the RR loader on the containernet fabric. One-time
manual DSM install, then `provision.sh` handles everything else.

## Bring-up

```bash
./fetch-rr.sh          # downloads out/rr.img (override RR_URL for mirrors)
./vm-up.sh             # boots on tap-dsm; VNC 127.0.0.1:5909, serial out/serial.log
```

## One-time DSM install (manual, ~15 min)

1. Watch `out/serial.log` (or VNC) until RR prints its config URL, then open
   `http://<vm-ip>:7681` (IP appears in `../net/run/dnsmasq.leases`, MAC
   `52:54:00:12:de:ad`).
2. In the RR configurator: model **DS3622xs+** (broadwellnk, same DSM 7
   kernel-4.4 family as the real DS1823xs+), version **7.2**, then
   *build the loader* and *boot*.
3. Open `http://<vm-ip>:5000`, let the installer download/upload the
   DS3622xs+ 7.2 `.pat`, create the admin user.
4. In DSM: Control Panel → Terminal & SNMP → **enable SSH**; Control
   Panel → Shared Folder → create **seaweed** on Volume 1 (create the
   btrfs/ext4 volume on the data disk first via Storage Manager).

## Provision + join

```bash
DSM_USER=admin DSM_PASS=... ./provision.sh                        # bundled weed
DSM_USER=admin DSM_PASS=... WEED_IMAGE=172.21.240.10:5000/seaweedfs:fork-large-disk \
  WEED_PLAIN_HTTP=true ./provision.sh                             # fork leg
DSM_USER=admin DSM_PASS=... WEED_IMAGE=chrislusf/seaweedfs:latest ./provision.sh  # upstream leg
```

`provision.sh` installs the SPK, writes `volume.yaml` + kube token/CA,
adds the pod-CIDR route via the kind node, and restarts the package.
Re-run it any time; it is idempotent and re-asserts the route.

## Teardown

`kill $(cat out/qemu.pid)` stops the VM; disks stay in `out/` for the
next boot. Delete `out/data.qcow2` for a factory-fresh DSM.
