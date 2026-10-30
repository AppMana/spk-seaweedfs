# DSM VM lab

Two things live here: the inputs for the Labcontainers DSM gates in
`lab/native` (`make test-dsm-*`), and an older manual lab driven by
`lab/Makefile`.

## Inputs for the DSM gates

The gates boot a private DSM VM and a tools peer on the documentation subnet
`192.0.2.0/24` with no default route. They need:

- `DSM_PRIVATE_DIR`: private copies of an RR loader image and a DSM data disk,
  made by
  `lab/dsm/prepare-private.sh RR_SEED DSM_SEED OUTPUT_PARENT`. It copies both
  seeds (reflinks where possible), checks their hashes, creates a random lab
  administrator in `account.env`, and installs `private-account-hook.sh` and
  `private-network.sh` into the copy with `guestfish`, so the account and the
  static lab address exist only on that disk. It never boots anything. Use a
  fresh copy for each destructive gate; the seeds stay read-only.
- `DSM_VM_IMAGE`: the DSM wrapper image. `Dockerfile` adds `launch-dsm.py` to
  the Labcontainers VM base; it boots the private disks with one declared NIC
  and no management NIC or published ports. `build-qemu.sh EXISTING_OUTPUT_PARENT IMAGE_TAG`
  builds the image with a QEMU 10.0.8 that carries `qemu-ncq-fix.patch`: an
  AHCI NCQ command accepted after a failed non-NCQ command (DSM probes an
  unsupported SMART log page) replayed the old error and left `PxCI` set. The
  script proves the AHCI qtest fails with `qemu-ncq-regression.patch` and
  passes, with the full AHCI suite, after the fix.
- `DSM_PEER_IMAGE` and `DSM_PEER_IMAGE_ID`: the tools peer that runs the SSH
  and HTTP probes. Containerlab needs the named tag; the gate checks it
  against the explicit image ID.

  ```sh
  docker build -f lab/dsm/Dockerfile.peer -t labcontainers/dsm-peer:lab lab/dsm
  export DSM_PEER_IMAGE=labcontainers/dsm-peer:lab
  export DSM_PEER_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$DSM_PEER_IMAGE")
  ```

- `LABCONTAINERS_LABD`: the Labcontainers daemon matching the SDK in
  `lab/native/go.mod`.

The upgrade gates also take `DSM_KUBERNETES_INPUTS` and the baseline and
candidate packages; see [AGENTS.md](../../AGENTS.md).

## Manual lab (`lab/Makefile`)

A containernet fabric, a kind cluster with seaweedfs-operator, a local
registry and a DSM VM booted with `vm-up.sh`. It is for exploring by hand:
`vm-up.sh` reuses existing disks in `out/` and the `tap-dsm` interface, and
`provision.sh` installs whichever SPK it finds. Its results do not stand in
for the gates above.

```bash
make -C lab fabric-up registry-up cluster-up seaweed-fork
make -C lab vm-up                 # fetches out/rr.img; VNC 127.0.0.1:5909, serial telnet 127.0.0.1:5809
```

First boot, entirely over SSH to the RR loader (find its DHCP lease for MAC
`52:54:00:12:de:ad` in `lab/net/run/dnsmasq.leases`):

```bash
lab/dsm/configure-rr.sh                               # model DS3622xs+, DSM 7.2, build the loader
DSM_USER=... DSM_PASS=... lab/dsm/install-dsm.sh      # internet install; wipes the data disk
# once DSM is up, boot back into RR (menu.sh option z, or power cycle into the loader)
DSM_USER=... DSM_PASS=... lab/dsm/inject-admin.sh     # stage the admin account and SSH
# boot DSM again and log in over SSH
DSM_USER=... DSM_PASS=... lab/dsm/create-storage.sh   # pool, btrfs volume and data directory, idempotent
```

`inject-admin.sh` mounts the DSM system partition from RR, which exists only
after DSM has installed once.

```bash
DSM_USER=... DSM_PASS=... make -C lab provision-bundled    # bundled weed
DSM_USER=... DSM_PASS=... make -C lab provision-fork       # fork image from the lab registry
DSM_USER=... DSM_PASS=... make -C lab provision-upstream   # upstream image
make -C lab verify-bundled
```

`provision.sh` installs the SPK, writes `volume.yaml` and the token and CA,
adds the pod CIDR route through the kind node and restarts the package; it is
idempotent. The NAS appears in `volume.list` under `dataCenter: lab,
rack: synology`. `kill $(cat lab/dsm/out/qemu.pid)` stops the VM and keeps
its disks; delete `out/data.qcow2` for a factory-fresh DSM. `make -C lab down`
tears everything down except the registry and images.
