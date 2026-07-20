# spk-seaweedfs

A Synology DSM 7 package that runs a [SeaweedFS](https://github.com/seaweedfs/seaweedfs) `weed volume` daemon on a Synology NAS so the box participates in volume serving for an existing [seaweedfs-operator](https://github.com/seaweedfs/seaweedfs-operator)-managed cluster.

The package authenticates to the Kubernetes apiserver with a service-account token to discover master endpoints (and, optionally, mTLS material), renders `weed volume` argv from a single config file, then execs the upstream `weed` binary. SeaweedFS does not authenticate volume↔master heartbeats, so cluster membership is gated by LAN reachability plus the `dataCenter` / `rack` / `ip` / `publicUrl` the volume self-reports. The kube token is purely for discovery.

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

## Configuring

Single source of truth is `/var/packages/seaweedfs/var/volume.yaml`. The DSM wizard writes this file from form inputs; the SSH path edits it directly. They are bit-for-bit equivalent. See [docs/configure-cli.md](docs/configure-cli.md).

## Sourcing `weed` from an OCI image

By default the SPK runs the `weed` bundled at build time. Setting `weed.image`
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

## How it joins the cluster

1. `service_prestart` runs `synology-volume-bootstrap`.
2. The bootstrap loads `volume.yaml`, builds a kube REST client from the bearer token, GETs `seaweeds.seaweed.com/<name>` plus the master `Service` / `Endpoints` in that namespace, and emits `weed volume` argv to `/var/packages/seaweedfs/var/run/argv`.
3. `service_prestart` reads that file into a bash array and execs `weed volume "${args[@]}"`.
4. The volume daemon opens its bidirectional gRPC heartbeat stream to a master and is enrolled into the cluster topology under the `dataCenter` / `rack` labels from `volume.yaml`. From a pod inside the cluster, `weed shell volume.list` then shows the Synology.

There is no in-cluster controller and no new CRD. The seaweedfs-operator is unaware of the Synology beyond what it can see through `weed shell`. This is the v0.1 scope; future releases may add a `SynologyVolume` CR for status surfaces.

## Limitations

- DSM 7 x86_64 only.
- Volume role only. No filer, no S3 gateway.
- The kube token is a long-lived bearer token (no projected-token rotation).
- Operator-side ingress / `volume.ingress` is unrelated to this package; we advertise on the LAN directly.

## License

Apache-2.0. See [LICENSE](LICENSE). Bundled `weed` is also Apache-2.0.
