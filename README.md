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

`make test` requires all 25 reviewed bootstrap tests to pass (no missing tests
or skips), plus supervisor, source, metadata and runner-contract checks. PR CI
also builds the x64 DSM 7.2 SPK and inspects its INFO version/architecture,
executable lifecycle hooks, x86-64 payloads and component hashes. Run
`python3 tests/package_artifact.py` to inspect the exact version/revision named
by the package Makefile; it never selects an arbitrary older SPK.

Tag releases reuse these checks and upload the same built artifact, without
rebuilding it. The tag must be `v<SPK_VERS>-<SPK_REV>`. Publication is blocked
unless repository Actions variable `DSM_QUALIFIED_SPK_SHA256` matches that
artifact's SHA-256. Leave this unset until the exact downloaded CI artifact
passes isolated DSM install/forward-upgrade/reboot/crash/uninstall-preservation tests.
After qualification, set the digest and rerun only the failed publication job
so the already-tested bytes are reused; rerunning the build requires fresh
qualification. This temporary promotion gate does not itself execute DSM tests.
Never set the variable merely because source tests or package inspection pass.

The current package definition is the **lab-only 4.47-12 candidate**, pinning
SeaweedFS `8ecd3e03f9fb7b4361cce12cd439520bfef00ca1`, including durable-index
replay fixes and cgroup-derived memory/admission sizing. The old `4.40-4` SPK
remains the upgrade-test baseline. Do not deploy or set
`DSM_QUALIFIED_SPK_SHA256` until the exact candidate artifact has passed real
DSM install, restart/crash, forward upgrade, and data-preserving
uninstall tests. Component tests and structural SPK inspection do not satisfy
that gate. The binary embeds its full source SHA, not just a package label.

Downgrading to a legacy SPK is not a deployment requirement. Keep backups and
the retained baseline for compatibility investigations; do not uninstall a
working candidate to qualify an unnecessary downgrade.

For a new candidate, update `PKG_VERS`/`PKG_COMMIT` and the verified archive
digests in `cross/seaweedfs/`, `SPK_VERS`/`SPK_REV` in
`diyspk/seaweedfs/Makefile`, and the reviewed source expectations in
`tests/package-source.sh`. Keep the baseline SPK and both artifact manifests;
the exact candidate path is derived by `tests/package_artifact.py`, never by
selecting an arbitrary SPK from the build directory.

The local 2026-09-23 build completed and passed `tests/package_artifact.py`:
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

Multiple instances share one package cgroup: they retain a conservative 3072 MiB
Go budget **divided by `volume.instances`** (1536 MiB each for two), with admission
derived from each process's budget. Otherwise each process would independently
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
