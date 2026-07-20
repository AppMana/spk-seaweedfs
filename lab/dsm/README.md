# xpenology DSM VM (lab)

QEMU/KVM VM booting the RR loader on the containernet fabric. Every
step below is scripted and was validated end-to-end on 2026-07-20 (DSM
7.2.2-72806, DS3622xs+/broadwellnk): the VM joined a seaweedfs-operator
kind cluster and served a real read/write from `/volume1/seaweed`.

## Bring-up

```bash
./fetch-rr.sh          # downloads out/rr.img (override RR_URL for mirrors)
./vm-up.sh             # boots on tap-dsm; VNC 127.0.0.1:5909, serial telnet 127.0.0.1:5809
```

## First boot: configure RR, install DSM, inject admin+SSH

RR (root:rr over SSH once it's up — check `../net/run/dnsmasq.leases`
for its DHCP lease on MAC `52:54:00:12:de:ad`) exposes its whole flow
over SSH; none of this needs the web configurator or VNC:

```bash
./configure-rr.sh                              # model DS3622xs+, DSM 7.2.x, build loader
DSM_USER=swadmin DSM_PASS='...' ./inject-admin.sh   # stage admin+SSH for DSM's first boot
```

`configure-rr.sh` builds the loader but does not reboot into it —
`inject-admin.sh` needs the DSM system partition mounted from the RR
side first (it doesn't exist until DSM installs, so this only works
*after* a first DSM install has happened once; on a truly fresh disk,
boot DSM, let the installer run, then reboot back to RR once to inject,
then boot DSM again — `configure-rr.sh`'s final `boot` step handles the
first install). Then kick off the actual DSM install:

```bash
DSM_USER=swadmin DSM_PASS='...' ./install-dsm.sh    # internet install, wipes the data disk, ~10 min incl. reboot
# wait for the VM to come back up (watch http://<vm-ip>:5000 respond as DSM, not the installer)
DSM_USER=swadmin DSM_PASS='...' ./inject-admin.sh    # NOW mount succeeds; re-trigger via RR if needed
```

In practice the simplest reliable order is: `configure-rr.sh` → `install-dsm.sh`
→ once DSM is up, reboot to RR (`menu.sh` option `z`/Junior or power-cycle
into the loader) → `inject-admin.sh` → boot DSM again. Login as
`swadmin` over SSH once that completes.

## Storage: pool, volume, data directory

```bash
DSM_USER=swadmin DSM_PASS='...' ./create-storage.sh
```

Creates a single-disk storage pool + btrfs volume on the data disk
(`sdb` by default) via the same webapi calls DSM's Storage Manager UI
makes, then `mkdir -p /volume1/seaweed`. Idempotent — skips pool/volume
creation if `/volume1` already exists.

## Provision + join

```bash
DSM_USER=swadmin DSM_PASS=... ./provision.sh                        # bundled weed
DSM_USER=swadmin DSM_PASS=... WEED_IMAGE=172.21.240.10:5000/seaweedfs:fork-large-disk \
  WEED_PLAIN_HTTP=true ./provision.sh                               # fork leg
DSM_USER=swadmin DSM_PASS=... WEED_IMAGE=chrislusf/seaweedfs:latest ./provision.sh   # upstream leg
```

`provision.sh` installs the SPK, writes `volume.yaml` + kube token/CA,
adds the pod-CIDR route via the kind node, and restarts the package.
Re-run it any time; it is idempotent and re-asserts the route. Verify
with `weed shell`'s `volume.list` from inside the cluster, or `GET`
`/dir/status` off any master pod — the synology should show up under
`dataCenter: lab, rack: synology`.

## Teardown

`kill $(cat out/qemu.pid)` stops the VM; disks stay in `out/` for the
next boot. Delete `out/data.qcow2` for a factory-fresh DSM.
