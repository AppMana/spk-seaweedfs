# spk-seaweedfs

A Synology DSM 7 package that runs a [SeaweedFS](https://github.com/seaweedfs/seaweedfs) `weed volume` daemon on a Synology NAS so the box participates in volume serving for an existing Kubernetes-managed SeaweedFS cluster — either a [seaweedfs-operator](https://github.com/seaweedfs/seaweedfs-operator)-managed one (via its Seaweed CR), or a plain Helm-chart deployment with no operator installed at all (via `kube.masterService`, reading the master Service's Endpoints directly).

The package authenticates to the Kubernetes apiserver with a service-account token to discover master endpoints (and, optionally, mTLS material), renders `weed volume` argv from a single config file, then execs the upstream `weed` binary. SeaweedFS does not authenticate volume↔master heartbeats, so cluster membership is gated by LAN reachability plus the `dataCenter` / `rack` / `ip` / `publicUrl` the volume self-reports. The kube token is purely for discovery — set `kube.seaweedName` for the operator-CRD path, or `kube.masterService` (+ optional `kube.masterPort`, default 9333) for the CRD-free path; `masterService` wins if both are set.

`weed` is built with the upstream `5BytesOffset` build tag so each volume can grow to 8000 GB instead of the 30 GB default; this is the right choice for DAS-attached Synology storage. Verify with `weed version` inside the SPK; the line should read `version 8000GB 4.23 ...`. The rest of the cluster's masters/filers must also be running 5-byte builds — operator-managed pods are typically 30 GB unless you have already pinned `5BytesOffset` upstream.

## Repo layout

```
.
├── cmd/synology-volume-bootstrap/   Go program that GETs the Seaweed CR, renders argv
├── cross/seaweedfs/                 spksrc cross-compile of upstream `weed`
├── diyspk/seaweedfs/                SPK metadata + wizard + service-setup
├── kube/                            ServiceAccount + Role + RoleBinding for the Synology
├── docs/                            install / configure / verify guides
├── spksrc/                          submodule: SynoCommunity/spksrc
└── Makefile                         orchestrator (host build → stage → spksrc)
```

## Build host prerequisites

Linux build host (verified on Ubuntu 24.04 / 25.x):

```bash
sudo apt install --no-install-recommends -y \
  build-essential make wget rsync tar gzip \
  jq moreutils imagemagick \
  golang-go
```

`moreutils` (`sponge`) is required by spksrc's `service.mk` to atomically rewrite `conf/resource`; without it the build fails with exit 127 mid-way through the package step. `imagemagick` (`convert`) is used by spksrc to resize the package icon. `golang-go` is the host Go that builds our `synology-volume-bootstrap` binary; spksrc itself fetches its own Go toolchain into the build tree for cross-compiling `weed`.

## Building the SPK

```bash
git clone --recurse-submodules https://github.com/appmana/spk-seaweedfs.git
cd spk-seaweedfs
make            # builds bootstrap (host Go), stages into spksrc, runs spksrc
make show-spk   # prints the path of the produced .spk
```

First build downloads the Synology x64 cross-toolchain (~hundreds of MB, cached after) and the SeaweedFS Go module graph. Subsequent builds reuse caches and complete in ~2-3 minutes on this hardware. The produced SPK lands at `spksrc/packages/seaweedfs_x64-7.2_4.23-1.spk` and covers every DSM 7.2 x86_64 Synology family (apollolake, denverton, geminilake, broadwell, etc.) in one file.

By default this builds for `arch-x86_64-7.2`. Override with `make TARGET_ARCH=x86_64 TARGET_DSM=7.0 spk` if needed.

The bootstrap binary is built once with the host's Go (`CGO_ENABLED=0 GOOS=linux GOARCH=amd64`) and staged into `diyspk/seaweedfs/src/bin/` before spksrc runs. spksrc's own Go cross-compile pipeline handles the `weed` binary.

## Installing

See [docs/install-cli.md](docs/install-cli.md) for the SSH-based path. The DSM Package Center route works the same way once `Trust Level: Any publisher` is enabled (or via the one-time confirmation dialog on DSM 7.2+).

## Package qualification and publication

`make test` requires all 33 reviewed bootstrap tests to pass (no missing tests
or skips), plus supervisor, source, metadata and runner-contract checks. PR CI
also builds the x64 DSM 7.2 SPK and inspects its INFO version/architecture,
executable lifecycle hooks, x86-64 payloads and component hashes. Run
`python3 tests/package_artifact.py` to inspect the exact version/revision named
by the package Makefile; it never selects an arbitrary older SPK.
For a target NAS, add `--dsm-version 7.4.1-90080 --dsm-arch v1000`, replacing
both values with the observed DSM release/build and Synology architecture.
This read-only check rejects incompatible package metadata; it does not prove
installation, service registration or runtime compatibility on that DSM release.

Tag releases reuse these checks and upload the same built artifact, without
rebuilding it. The tag must be `v<SPK_VERS>-<SPK_REV>`. Publication is blocked
unless repository Actions variable `DSM_QUALIFIED_SPK_SHA256` matches that
artifact's SHA-256. Leave this unset until the exact downloaded CI artifact
passes isolated DSM install/forward-upgrade/reboot/crash/uninstall-preservation tests.
After qualification, set the digest and rerun only the failed publication job
so the already-tested bytes are reused; rerunning the build requires fresh
qualification. This temporary promotion gate does not itself execute DSM tests.
Never set the variable merely because source tests or package inspection pass.

The current package definition is **4.48-9**: the 4.48-8 package with
transfer admission shared equally among the automatically sized servers rather
than by the volumes they hold (see Resource limits). Under 4.48-8 the NAS's new
`/volume4` server, holding 8 volumes, got a 509 MiB Go limit and 93 MiB of
upload admission; replicate POSTs waited up to 25 s for it and the primaries'
5 s no-progress deadline failed writes. The CI artifact with SHA-256
`ca591824a13916c63097bb92fa255be35442ee7061ecf9ccf2cbb7a4f235cdba` (`weed`
unchanged) passed `make test-dsm-complete` (404 s) and
`make test-dsm-volume-servers` (379 s) as a package-only upgrade from the
deployed 4.48-8, and runs on the NAS since 2026-10-26 with 423 MiB upload and
141 MiB download admission on each of its four servers.

4.48-8: the 4.48-7 package with memory
sized from the installed RAM (see Resource limits): the package limit is the
installed memory less DSM's share, and servers without `memoryMB` share it by
the volumes each holds; `auto` in the wizard. The CI artifact with SHA-256
`357915b2b3430858143297d8445c8edd2bb04832ca31fcd5e38a61291db0fe1a` (`weed`
`1cc2ff36cd5f6a9030f0e54b5b5aaa2bbffd59943d53e53944993d720ea8a6e4`) passed
`make test-dsm-complete` (364 s) and `make test-dsm-volume-servers` (360 s;
on the 4 GB lab DSM a fixed 1024 MiB server beside an automatic one at
379 MiB, limit 1870 MiB) as a package-only upgrade from the deployed 4.48-7.

4.48-7: the 4.48-6 `weed` with packaging
that keeps volume data out of DSM shared folders. The install wizard no longer
asks for a shared folder and the package declares no `data-share` resource, so
DSM creates none; a fresh install runs its server from `/volumeN/@seaweedfs` on
the lowest-numbered volume, and new servers default to `@seaweedfs` too. The CI
artifact with SHA-256
`03dd363b7111ac92de8f3f538f2d4e6ebad93474fa03322edc7ab504030e7c88` (`weed`
`1cc2ff36cd5f6a9030f0e54b5b5aaa2bbffd59943d53e53944993d720ea8a6e4`) passed
`make test-dsm-complete` (384 s, including the fresh-install checks that DSM's
shared folders are unchanged and the data directory is a package-owned `@`
directory) and `make test-dsm-volume-servers` (367 s) as a package-only upgrade
(`DSM_PACKAGE_ONLY_UPGRADE=1`) from the deployed 4.48-6.

4.48-6: SeaweedFS
`6a570503378e408c57c74f36efda148c5950595c`, the build every component of the
AppMana cluster runs, with the 4.48-5 packaging and go-fuse pin. Over 27afab3bb it
changes only the filer, mount and shell; the volume server code is identical.
The CI artifact with SHA-256
`640ec78f795eb91362b3d8146d5ce6933526dc40de8486ac85872cc57b2d9e13` (`weed`
`1cc2ff36cd5f6a9030f0e54b5b5aaa2bbffd59943d53e53944993d720ea8a6e4`) passed
`make test-dsm-complete` (366 s) and `make test-dsm-volume-servers` (360 s) as a
binary upgrade from the deployed 4.48-5.

4.48-5 kept the 4.48-2 SeaweedFS
and go-fuse pins (the same `weed` binary) and added `volume.servers`, one server per
DSM volume chosen in the install/upgrade wizard, each with its own ports and memory
budget, and the unit's memory limit set to their sum. The CI artifact with SHA-256
`641fb8b06986e6b1b1568d7234bb9f7e679d6ce837ad4303def470fbf9b0fbfb` passed the
Labcontainers DSM lifecycle gate (`make test-dsm-complete`: forward upgrade from
4.47-15, readback across restart and abrupt crash, Kubernetes join, uninstall
retention, fresh install) and the two-server gate (`make test-dsm-volume-servers`).
It also passed both gates as a package-only upgrade from the deployed 4.48-2
(SHA-256 `c638bc4711c71397494f20ec371f74e7de47d537d86691c4513e62063a90a99d`), the
production upgrade path: the same lifecycle (readback after the upgrade, restart
and abrupt crash, retained `volume.yaml`/kube token/CA, registered unit limits,
Kubernetes join, uninstall retention, fresh install), and on a separate private
VM the switch to two servers after that upgrade.

The upgrade gate swaps the `weed` binary under existing data by default and
refuses a candidate with the baseline's `weed`. Packaging-only changes (wizard,
service scripts, registration) set `DSM_PACKAGE_ONLY_UPGRADE=1`, which instead
requires the identical `weed` and a different package version; the mode is
explicit, never inferred from equal hashes.

The 4.48-2 package definition pinned
SeaweedFS `27afab3bb0b39726df5f17afa4f1454203bba69f` and go-fuse
`6ead27e20708423a00718812de796241659ad387`. Over 4.48-1 it holds each
upload body in exactly its admitted bytes (a full 4 MiB body no longer
doubles its buffer to read EOF, which let stalled uploads hold twice the
admission budget and exhaust the memory limit), and `VolumeCopy` keeps the
existing replica until the copy is complete and mounted.
4.48-1 pinned `e7a60962653315b3c25aee65238030d06d7d18da` (upstream 4.48 base);
over 4.47-16 it added upstream 4.48 and writer-side avoidance of a stalled
volume server: a stalled chunk upload or S3 chunk copy moves to a new
assignment, and the master assigns a volume that avoids the servers the
writer saw stall.
4.47-16 pinned `5bd2ac835d6668537a749febb89888467977867f`; over 4.47-15 it added
`VolumeCopy` of a source volume with deletes since load (the copy no longer
fails its file-count check after removing the local replica) and an upload
deadline that abandons a volume server write only when it stops making
progress, so a slow but healthy NAS keeps its uploads. 4.47-15 pinned
`e1aaa5124781c6756be11cf0a722876813ca1c82`, with durable-index
replay fixes, cgroup-derived memory/admission sizing, guarded missing-record
repair, and refusal to vacuum an unindexed data tail. The qualified `4.47-15` SPK
is the forward-upgrade test baseline. Do not set
`DSM_QUALIFIED_SPK_SHA256` until the exact candidate artifact has passed real
DSM install, restart/crash, forward upgrade, and data-preserving
uninstall tests. Component tests and structural SPK inspection do not satisfy
that gate. The binary embeds its full source SHA, not just a package label.
The previous **4.47-14** SPK with SHA-256
`cafaf1646e31e7e987bea48ac6e5f6c0919080109e4b4e8a59eb381b85f1e5f8`
passed the isolated DSM 7.2 package lifecycle gate, including forward upgrade,
restart, abrupt VM crash, original-data readback and Kubernetes registration.
DSM 7.4.1 metadata eligibility does not extend that lifecycle result to
DSM 7.4.1 or to later artifacts.

The **4.47-15** artifact with SHA-256
`389a7ff91b59dd6cb288b6e52a76cc1b2d5e2a341ca8ea35d1db6715fbf0233f`
has separately passed that isolated DSM 7.2 lifecycle gate, including the
forward upgrade from 4.47-14 and abrupt VM crash/recovery. Its bundled `weed`
SHA-256 is `1fdc61adc091c4ea69c643d38dd3211c8c41c9b8c007f1f0bb31a4e63f8aeb7b`.
Production installation still requires DSM administrator authentication;
DSM 7.4.1 has passed metadata preflight, not this VM runtime qualification.

Downgrading to a legacy SPK is not a deployment requirement. Keep backups and
the retained baseline for compatibility investigations; do not uninstall a
working candidate to qualify an unnecessary downgrade.

For a new candidate, update `PKG_VERS`/`PKG_COMMIT` and the verified archive
digests in `cross/seaweedfs/`, `SPK_VERS`/`SPK_REV` in
`diyspk/seaweedfs/Makefile`, and the reviewed source expectations in
`tests/package-source.sh`. Keep the baseline SPK and both artifact manifests;
the exact candidate path is derived by `tests/package_artifact.py`, never by
selecting an arbitrary SPK from the build directory.

Historical evidence (not the current candidate): the local 2026-09-23 build
completed and passed `tests/package_artifact.py`:
`seaweedfs_x64-7.2_4.47-1.spk`, SHA-256
`6dc1b62e6c2c6d9ed751709571a06c024d323ea7480068f482af0d004c46b344`.
Its static x86-64 weed payload SHA-256 is
`ccc2ef604580082c3ae6d1b87cfbe70fd069f65c367176fbc2e73b5869bc0ce7`;
`weed version` reports `8000GB 4.47` and the pinned full source SHA. All 25
bootstrap cases, supervisor/source/metadata contracts and artifact rejection
tests passed. This artifact includes the existing local comment-only supervisor
edits and is lab evidence, not a clean release build or DSM qualification.
The retained `4.40-4` baseline SHA-256 is
`1a1c230d4f26649ab34b015766a5fdb9ed4950733b71ab79afe2418536497e05`.

## Configuring

Single source of truth is `/var/packages/seaweedfs/var/volume.yaml`. The DSM wizard writes this file from form inputs; the SSH path edits it directly. They are bit-for-bit equivalent. See [docs/configure-cli.md](docs/configure-cli.md).

## Sourcing `weed` from an OCI image

By default the SPK runs the large-disk `weed` bundled at build time. Release
4.40-3 pins AppMana SeaweedFS 4.40 post.2 and its AppMana go-fuse sibling by
commit and verifies both source archives during the build. `make test` checks
that these pins cannot drift away from the package version. Setting `weed.image`
in `volume.yaml` (or the wizard field) makes the bootstrap pull that OCI image
at package start, extract the configured `weed.binaries` (default
`/usr/bin/weed`), cache them by manifest digest under `weed.cacheDir`
(default `$SYNOPKG_PKGVAR/oci`), and exec the extracted binary instead. This
switches a NAS between SeaweedFS builds — e.g. a fork's `_large_disk` image
and genuine upstream — with a config edit plus `synopkg restart seaweedfs`,
no repackaging. `weed.digest` pins the manifest; `weed.plainHTTP: true`
allows local registries without TLS. Restarts reuse the digest cache without
network access, and when a mutable tag can't be resolved offline the
bootstrap falls back to the last extracted image. Clearing `weed.image`
reverts to the bundled binary on the next restart.

## Resource limits

With one volume instance, the supervisor leaves `GOMEMLIMIT` unset and omits
both transfer admission flags. The bundled memory-aware core derives its Go
soft limit and upload/download budgets from the effective cgroup limit, capped
at physical RAM. With 5 GiB available this means a 4.5 GiB Go soft limit and
2496 MiB upload / 832 MiB download admission. The read buffer remains 1 MiB.

### One server per DSM volume

`volume.servers` runs one `weed volume` process per entry, each from its own
directory (normally its own DSM volume, so each disk is a separate data node
in the same rack) with its own ports, `-metricsPort`, `max` and memory budget:

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
shared `extraFlags`. A server's ports are its identity in the cluster topology:
a server that stays keeps them, and a new one takes ports above every port used
before, so it never takes over a removed server's identity.

The install and upgrade wizards' **Volume servers** step lists every mounted
DSM volume with a checkbox, the server's directory and its memory budget,
pre-filled from the current configuration. A new server's directory defaults to
`/volumeN/@seaweedfs` (or the current servers' `@` name): DSM never registers
`@` directories as shared folders, whereas a plain top-level directory can be
restored as a share at mount and renamed on a name collision (`seaweed` to
`seaweed_1`), which leaves its server without its data. Leaving every box
unchecked keeps the current servers; a fresh install then runs one server from
`@seaweedfs` on the lowest-numbered volume. The package creates no DSM shared
folder. Unchecking a server stops it; its directory and data stay.
Run `register-service.sh` as an administrator afterwards, as after every
install or upgrade: it creates new server directories (only under a mounted
`/volumeN`) for the package user and applies the memory limit below.

**Memory.** The package's limit follows the installed memory: MemTotal less
DSM's share (2 GiB, or a quarter of a machine with more than 8 GiB), so an
8 GB NAS gets 5.7 GiB and a 16 GB one 12 GiB. `register-service.sh` writes it
as the unit's `MemoryLimit` (a drop-in beside the unit); a single legacy
`volume.dir` server keeps the unit's own 5 G limit. 1024 MiB of it (a quarter
of a limit under 4 GiB) stays outside the Go budgets, because cgroup v1
charges page cache to the package.

A server's `memoryMB` fixes its Go soft limit (`GOMEMLIMIT`); the core
derives its upload and download admission from it. Without `memoryMB` the
server is sized automatically at each package start: each server without one
holds its index, 256 MiB plus 2 MiB per volume it holds (a LevelDB index costs
about 2 MB per volume; a server with 746 volumes measured 1.4 GB resident,
2.3 GB peak), and the rest of the limit is shared equally among them for
transfers, passed as `-concurrentUploadLimitMB` and
`-concurrentDownloadLimitMB` (3:1) unless `extraFlags` sets them. Transfers are
not split by volumes held: the master grows new volumes on the emptiest disk,
so a nearly empty server takes most writes and replicas, and the core's own
admission, which assumes a 1.25 GiB base heap, leaves a small budget at its
floor. On the 8 GB NAS with 743, 26, 4 and 8 volumes that is 2306, 872, 828
and 836 MiB, each with 423 MiB upload and 141 MiB download admission. The
shares move as volumes are created, at the next start. Budgets that cannot fit the installed
memory (fixed budgets plus 256 MiB per automatic server plus the page cache
reserve) are refused by `--print-memory-limit-mb`, so registration fails
rather than set a limit above the machine; after a memory change, run
`register-service.sh` again. In the wizard, `auto` selects automatic sizing.

`volume.instances` (several servers in one directory) still divides a
conservative 3072 MiB Go budget among its processes (1536 MiB each for two),
with admission derived from each process's budget. Otherwise each process would independently
claim 90% of the same shared limit. An explicit `GOMEMLIMIT` remains a per-process
override; `volume.extraFlags` can explicitly override admission. Empty
`GOMEMLIMIT` is removed to enable automatic sizing. The Go limit is soft and
does not bound filesystem cache, mmap'd index files, or total process memory.
If overriding `weed.image` with an older/upstream build without automatic memory
sizing, explicitly configure compatible memory and admission limits; these
automatic defaults require the pinned AppMana memory fix.

DSM installs the bundled `conf/systemd/pkg-seaweedfs-volume.service` into
`/usr/local/lib/systemd/system/`. The service runs as `sc-seaweedfs`; installation
hooks remain unprivileged. DSM copies this unit but does **not** enable its
dependency on package startup. After installing/upgrading the SPK, register it
as an administrator before starting the package:

```sh
sudo sh /var/packages/seaweedfs/target/bin/register-service.sh
sudo synopkg start seaweedfs
```

Registration is idempotent and does not start a daemon. Run it after every
install or upgrade: it flushes installed package files and service registration
before reporting success, so an immediate power loss cannot rely on pending
filesystem writes. If registration fails, do not treat installation as ready.
Normal Package Center
and `synopkg` start/stop controls then manage the bounded, unprivileged unit.
The SPK alone is not a complete installation without registration. Its resource
settings are:

```ini
[Service]
LimitNOFILE=65536
MemoryAccounting=true
MemoryLimit=5G
LimitNPROC=4096
```

DSM's generated package-control unit defaults to a **4096 hard** descriptor
limit. The separate daemon unit raises this before switching to the package
user. Keep `LimitNOFILE` below `fs.nr_open`; an unprivileged process cannot
raise its inherited hard limit.

DSM 7.2's systemd 219 requires `MemoryLimit`, not `MemoryMax`; it does not
support `TasksMax`. `LimitNPROC` instead bounds threads/processes belonging to
the dedicated package UID, across all instances. The memory limit covers the
whole daemon unit, not each instance separately. Check the running processes
as well as systemd's loaded settings:

```sh
systemctl show pkg-seaweedfs-volume.service -p User -p LimitNOFILE -p MemoryLimit -p LimitNPROC
grep 'memory limits:' /var/packages/seaweedfs/var/log/weed.log | tail -2
grep 'nofile soft=' /var/packages/seaweedfs/var/log/weed.log | tail -2
```

Package upgrades reinstall the bundled unit through DSM's package manager;
the package does not attempt to write privileged drop-ins from its hooks.

## How it joins the cluster

1. DSM starts the package's daemon unit; `service_prestart` removes stale argument files.
2. Instance zero's `run.sh` runs `synology-volume-bootstrap`. It reads `volume.yaml`, authenticates to Kubernetes, and discovers masters via `kube.masterService` or the operator's `seaweeds.seaweed.seaweedfs.com` resource, writing argument files under `/var/packages/seaweedfs/var/run/`.
3. Each instance's `run.sh` reads its argument file and supervises the selected `weed volume` binary.
4. The volume daemon opens its bidirectional gRPC heartbeat stream to a master and is enrolled into the cluster topology under the `dataCenter` / `rack` labels from `volume.yaml`. From a pod inside the cluster, `weed shell volume.list` then shows the Synology.

There is no in-cluster controller and no new CRD. The seaweedfs-operator is unaware of the Synology beyond what it can see through `weed shell`. This is the v0.1 scope; future releases may add a `SynologyVolume` CR for status surfaces.

## Limitations

- DSM 7 x86_64 only.
- Volume role only. No filer, no S3 gateway.
- The kube token is a long-lived bearer token (no projected-token rotation).
- Operator-side ingress / `volume.ingress` is unrelated to this package; we advertise on the LAN directly.

## License

Apache-2.0. See [LICENSE](LICENSE). Bundled `weed` is also Apache-2.0.
