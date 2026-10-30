# spk-seaweedfs

A Synology DSM 7 package that runs [SeaweedFS](https://github.com/seaweedfs/seaweedfs)
`weed volume` servers on a Synology NAS, so the NAS serves volumes for an
existing Kubernetes-hosted SeaweedFS cluster. The cluster can be managed by
[seaweedfs-operator](https://github.com/seaweedfs/seaweedfs-operator) (through
its `Seaweed` resource) or be a plain Helm deployment with no operator
(through `kube.masterService`, which reads the master Service's Endpoints).

The package authenticates to the Kubernetes API server with a service account
token to discover master addresses and, optionally, mTLS material, renders
`weed volume` arguments from one config file, then runs `weed`. SeaweedFS does
not authenticate volume to master heartbeats, so cluster membership rests on
LAN reachability plus the `dataCenter`, `rack`, `ip` and `publicUrl` the
server reports. The token is used for discovery only: set `kube.seaweedName`
for the operator path, or `kube.masterService` (and optionally
`kube.masterPort`, default 9333) for the plain path; `masterService` wins if
both are set.

The bundled `weed` is built from
[AppMana/forks-seaweedfs](https://github.com/AppMana/forks-seaweedfs) with the
`5BytesOffset` tag, so a volume can grow to 8000 GB instead of 30 GB;
`weed version` reports `8000GB`. Masters and filers in the cluster must also
run 5-byte builds.

## Repository layout

```
cmd/synology-volume-bootstrap/   Go program: reads volume.yaml, discovers masters, renders argv
cross/seaweedfs/                 spksrc cross-compile of the fork's weed (source pin and digests)
diyspk/seaweedfs/                SPK metadata, wizards, service scripts and systemd unit
kube/                            ServiceAccount, Role and RoleBinding for the NAS
docs/                            install, configure and verify guides
tests/                           package, supervisor and bootstrap test entry points
lab/native/                      DSM VM lifecycle gates (Labcontainers)
lab/dsm/                         DSM VM image tooling and the peer image for the gates
spksrc/                          submodule: SynoCommunity/spksrc
Makefile                         host build, stage into spksrc, build SPK, tests
```

## Building

Linux build host:

```bash
sudo apt install --no-install-recommends -y \
  build-essential make wget rsync tar gzip \
  jq moreutils imagemagick \
  golang-go
```

spksrc's `service.mk` needs `sponge` from `moreutils` (without it the package
step fails with exit 127) and `convert` from `imagemagick` to resize the icon.
The host Go builds `synology-volume-bootstrap`; spksrc downloads its own Go to
cross-compile `weed`.

```bash
git clone --recurse-submodules https://github.com/AppMana/spk-seaweedfs.git
cd spk-seaweedfs
make            # build the bootstrap, stage into spksrc, build the SPK
make show-spk   # print the path of the built .spk
```

The first build downloads the Synology toolchain and the SeaweedFS module
graph; later builds reuse them. The default target is `arch-x64-7.2`, which
produces `spksrc/packages/seaweedfs_x64-7.2_<SPK_VERS>-<SPK_REV>.spk` for every
DSM 7.2 x86_64 model. Override with `make TARGET_ARCH=x64 TARGET_DSM=7.0`.

## Installing

See [docs/install-cli.md](docs/install-cli.md) for installing over SSH. Package
Center works the same way once `Trust Level: Any publisher` is set.

After every install or upgrade, register the daemon unit as an administrator
before starting the package:

```sh
sudo sh /var/packages/seaweedfs/target/bin/register-service.sh
sudo synopkg start seaweedfs
```

DSM copies the bundled `pkg-seaweedfs-volume.service` into
`/usr/local/lib/systemd/system/` but does not enable it for package startup.
Registration is idempotent and starts nothing. It creates missing server
directories (only under a mounted `/volumeN`) for the package user, writes the
memory limit (below), and flushes the installed files and registration before
it reports success, so a power loss right after cannot leave a half-written
registration. If it fails, the installation is not ready. Package Center and
`synopkg` then control the unit, which runs as `sc-seaweedfs`; the install
hooks stay unprivileged.

## Configuring

`/var/packages/seaweedfs/var/volume.yaml` holds the whole configuration. The
wizard writes it from its form, and editing it over SSH is equivalent. See
[docs/configure-cli.md](docs/configure-cli.md).

### One server per DSM volume

`volume.servers` runs one `weed volume` process per entry, each from its own
directory (normally its own DSM volume, so each disk is a separate data node in
the same rack), with its own ports, `-metricsPort`, `max` and memory budget:

```yaml
volume:
  ip: 192.0.2.20
  rack: nas
  metricsPort: 9325        # first server's default; +i for the others
  servers:
    - dir: /volume1/@seaweedfs
      port: 8080
      grpcPort: 18080
      memoryMB: 3072
    - dir: /volume2/@seaweedfs  # port 8081, grpcPort 18081, metricsPort 9326
```

It replaces `volume.dir` and `volume.instances`. Per-server flags (`-dir`,
`-port`, `-port.grpc`, `-metricsPort`, `-max`, `-publicUrl`, `-disk`) cannot be
set in `extraFlags`. A server's ports are its identity in the cluster
topology: a server that stays keeps them, and a new server takes ports above
every port used before, so it never takes over a removed server's identity.

The install and upgrade wizards' **Volume servers** step lists every mounted
DSM volume with a checkbox, the server directory and its memory budget,
prefilled from the current configuration. A new server's directory defaults to
`/volumeN/@seaweedfs`: DSM never registers `@` directories as shared folders,
whereas it can restore a plain top-level directory as a share at mount and
rename it on a name collision, leaving its server without its data. Leaving
every box unchecked keeps the current servers; a fresh install then runs one
server from `@seaweedfs` on the lowest-numbered volume. The package creates no
shared folder. Unchecking a server stops it and keeps its directory and data.
Run `register-service.sh` afterwards.

### Memory

The package's memory limit is the installed memory less DSM's share (2 GiB,
or a quarter of a machine with more than 8 GiB), so an 8 GB NAS gets about
5.7 GiB and a 16 GB one 12 GiB. `register-service.sh` writes it as the unit's
`MemoryLimit` in a drop-in; a single legacy `volume.dir` server keeps the
unit's own 5G. 1024 MiB of the limit (a quarter of a limit under 4 GiB) stays
outside the Go budgets, because cgroup v1 charges page cache to the package.

A server's `memoryMB` sets its Go soft limit (`GOMEMLIMIT`), and the core
derives its transfer admission from it. Servers without `memoryMB` are sized
at each package start: each gets 256 MiB plus 2 MiB per volume it holds for
its index, and the rest of the limit is shared equally among them for
transfers, passed as `-concurrentUploadLimitMB` and
`-concurrentDownloadLimitMB` in a 3:1 ratio unless `extraFlags` sets them.
Transfers are shared equally rather than by volumes held because the master
grows new volumes on the emptiest disk, so a nearly empty server takes most
writes and replicas. Shares move as volumes are created, at the next start.
Budgets that cannot fit (fixed budgets, plus 256 MiB per automatic server,
plus the page cache reserve) make `--print-memory-limit-mb` fail, so
registration fails instead of setting a limit above the machine; after a
memory upgrade, run `register-service.sh` again. In the wizard, `auto` selects
automatic sizing.

A single server with no `memoryMB` leaves `GOMEMLIMIT` unset and omits the
admission flags; the core sizes itself from the cgroup limit, capped at
physical RAM. `volume.instances` (several servers in one directory) divides a
fixed 3072 MiB Go budget among its processes. An explicit `GOMEMLIMIT` is a
per-process override, an empty one is removed, and `volume.extraFlags` can
override admission. The Go limit is soft and bounds neither filesystem cache,
mmapped index files nor total process memory. These automatic defaults need
the fork's memory sizing; with an upstream `weed.image`, set memory and
admission explicitly.

### Unit limits

```ini
[Service]
LimitNOFILE=65536
MemoryAccounting=true
MemoryLimit=5G
LimitNPROC=4096
```

DSM's generated package control unit has a hard limit of 4096 descriptors;
the separate daemon unit raises it before switching to the package user. Keep
`LimitNOFILE` below `fs.nr_open`. DSM 7.2's systemd 219 needs `MemoryLimit`
rather than `MemoryMax` and has no `TasksMax`, so `LimitNPROC` bounds the
package user's threads across all servers. The memory limit covers the whole
unit, not each server. Package upgrades reinstall the unit through DSM; the
hooks never write privileged drop-ins. To check:

```sh
systemctl show pkg-seaweedfs-volume.service -p User -p LimitNOFILE -p MemoryLimit -p LimitNPROC
grep 'memory limits:' /var/packages/seaweedfs/var/log/weed.log | tail -2
grep 'nofile soft=' /var/packages/seaweedfs/var/log/weed.log | tail -2
```

### Running `weed` from an OCI image

Setting `weed.image` in `volume.yaml` (or the wizard field) makes the bootstrap
pull that image at package start, extract `weed.binaries` (default
`/usr/bin/weed`), cache them by manifest digest under `weed.cacheDir` (default
`$SYNOPKG_PKGVAR/oci`) and run the extracted binary instead of the bundled
one. This switches a NAS between SeaweedFS builds with a config edit and
`synopkg restart seaweedfs`. `weed.digest` pins the manifest, and
`weed.plainHTTP: true` allows a registry without TLS. Restarts reuse the
digest cache without network access, and when a mutable tag cannot be
resolved the bootstrap falls back to the last extracted image. Clearing
`weed.image` returns to the bundled binary on the next restart.

## How it joins the cluster

1. DSM starts the daemon unit; `service_prestart` removes stale argument files.
2. Server zero's `run.sh` runs `synology-volume-bootstrap`, which reads `volume.yaml`, authenticates to Kubernetes, discovers masters through `kube.masterService` or the operator's `seaweeds.seaweed.seaweedfs.com` resource, and writes argument files under `/var/packages/seaweedfs/var/run/`.
3. Each server's `run.sh` reads its argument file and supervises its `weed volume` process.
4. The volume server opens its heartbeat stream to a master and joins the topology under the configured `dataCenter` and `rack`; `volume.list` in `weed shell` then shows it.

There is no in-cluster controller and no CRD of its own.

## Limitations

- DSM 7 x86_64 only.
- Volume servers only; no filer or S3 gateway.
- The Kubernetes token is a long-lived bearer token, not a rotated projected token.
- The NAS advertises on the LAN directly; operator ingress settings do not apply.

## Development

See [AGENTS.md](AGENTS.md) for tests, the DSM lab gates and the release
procedure.

## License

Apache-2.0. See [LICENSE](LICENSE). The bundled `weed` is also Apache-2.0.
