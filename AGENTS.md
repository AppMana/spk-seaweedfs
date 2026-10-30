# Working on spk-seaweedfs

[README.md](README.md) describes what the package does. This file is how to
build, test and release it.

## Checkout and build

```sh
git clone --recurse-submodules https://github.com/AppMana/spk-seaweedfs.git
make            # bootstrap (host Go), stage into spksrc, build the x64 DSM 7.2 SPK
make show-spk
```

The source of truth is `cmd/`, `cross/` and `diyspk/` at the top level.
`make stage` copies them into the `spksrc` submodule, which is never edited
directly; edit the top-level copies.

## Tests

```sh
make test
```

runs, in order: `test-bootstrap` (the bootstrap Go tests through
`tests/bootstrap_suite.py`, which fails on any missing or skipped test in its
`REQUIRED` list; add new tests there), `test-supervisor` (`run.sh` restart
contract and bootstrap recovery), `test-package-source` (the source pins),
`test-package-metadata`, `test-runner-contracts` (`tests/test_*.py`) and
`test-native` (DSM lab topology and artifact contracts under `-race`, no VM).
CI (`.github/workflows/test.yml`) runs the same pieces on every push and pull
request, then builds the SPK and inspects it with `tests/package_artifact.py`.

`python3 tests/package_artifact.py` inspects the SPK named by the package
Makefiles (never an arbitrary file in the build directory). Add
`--dsm-version <version-build> --dsm-arch <arch>` to check the package
metadata against a specific DSM; that check is read-only and says nothing
about installation on that DSM.

## DSM lab gates

Real DSM in disposable Labcontainers VMs (`lab/native`). Inputs are prepared
as described in [lab/dsm/README.md](lab/dsm/README.md). Each target checks its
inputs are set:

| Target | Test | Inputs |
| --- | --- | --- |
| `make test-dsm-live` | private boot | `DSM_PRIVATE_DIR`, `DSM_VM_IMAGE`, `DSM_PEER_IMAGE`, `DSM_PEER_IMAGE_ID`, `LABCONTAINERS_LABD` |
| `make test-dsm-storage` | storage integrity | same |
| `make test-dsm-package` | install and upgrade | same, plus `DSM_BASELINE_SPK`, `DSM_BASELINE_SPK_SHA256`, `DSM_CANDIDATE_SPK`, `DSM_CANDIDATE_SPK_SHA256`, `DSM_BASELINE_VERSION`, `DSM_CANDIDATE_VERSION`, `DSM_CANDIDATE_WEED_SHA256` |
| `make test-dsm-fresh`, `make test-dsm-retention` | fresh install, uninstall keeps data and config | same, plus `DSM_CANDIDATE_SPK`, `DSM_CANDIDATE_SPK_SHA256` |
| `make test-dsm-kubernetes` | join a real Kubernetes backend | same, plus `DSM_KUBERNETES_INPUTS` |
| `make test-dsm-complete` | full lifecycle: upgrade under existing data, readback across restart and abrupt crash, Kubernetes join, uninstall retention, fresh install | the VM inputs, `DSM_KUBERNETES_INPUTS`, both SPKs with their SHA-256, `DSM_BASELINE_VERSION`, `DSM_BASELINE_WEED_SHA256`, optional `DSM_SEED_PACKAGE_VERSION` and `DSM_PACKAGE_ONLY_UPGRADE` |
| `make test-dsm-volume-servers` | two servers on separate directories, ports and budgets after the upgrade | same as `test-dsm-complete` |

`DSM_KUBERNETES_INPUTS` is a JSON file naming the pinned offline Kubernetes
backend images and media and the candidate's package version and `weed`
SHA-256. Use a fresh `DSM_PRIVATE_DIR` for each destructive gate.

The upgrade gates swap the `weed` binary under existing data and reject a
candidate with the baseline's `weed`. For a packaging-only change (wizard,
service scripts, registration) set `DSM_PACKAGE_ONLY_UPGRADE=1`, which
instead requires the identical `weed` and a different package version. The
mode is explicit, never inferred from equal hashes. A change to `weed`
needs `make test-dsm-complete` and `make test-dsm-volume-servers` as a binary
upgrade from the deployed package; a packaging change needs both in
package-only mode.

## Moving to a new SeaweedFS commit

The bundled `weed` is built from a source archive of a pinned
[forks-seaweedfs](https://github.com/AppMana/forks-seaweedfs) commit, so that
commit must stay reachable there. Update together:

- `cross/seaweedfs/Makefile`: `PKG_COMMIT` (full SHA), `PKG_VERS`, and
  `GO_FUSE_COMMIT`/`GO_FUSE_SHA256` when the fork's go-fuse sibling changes.
- `cross/seaweedfs/digests`: SHA1, SHA256 and MD5 of
  `seaweedfs-<PKG_VERS>.tar.gz`, the archive
  `https://github.com/AppMana/forks-seaweedfs/archive/<PKG_COMMIT>.tar.gz`.
- `diyspk/seaweedfs/Makefile`: `SPK_VERS` and `SPK_REV` (bump `SPK_REV` for
  every package change).
- `tests/package-source.sh`: the expected version, commit and go-fuse commit.

Then `make test-package-source`, `make` and the DSM gates.

## Release

1. Tag `v<SPK_VERS>-<SPK_REV>` on the commit CI built. `release.yml` reruns
   the tests and builds the SPK.
2. Its publish job uploads the built bytes only when repository variable
   `DSM_QUALIFIED_SPK_SHA256` equals their SHA-256. Run the DSM gates on that
   exact CI artifact, set the variable to its digest, then rerun only the
   failed publish job so the tested bytes are the ones published. Rebuilding
   produces new bytes that need the gates again.
3. `tests/prepare_release.py --spk <file> --tag <tag> --source <full sha> --results-root <dir>`
   creates a draft release from qualified bytes without rebuilding.

## Commits

- Concise one-line subjects; a package change names its version
  (`SeaweedFS Volume 4.51-2: fork 1e3820260`).
- Reasoning, gate results and artifact digests go in commit messages, not in
  the README. Docs describe current behaviour only.
- This repository is public: docs carry no internal hostnames, addresses or
  credentials, and gate inputs are referenced by name.
