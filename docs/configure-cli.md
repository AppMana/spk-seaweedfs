# Configuring (post-install)

The package's behavior is fully described by `/var/packages/seaweedfs/var/volume.yaml`. The DSM wizard writes this file on first install; everything else (mTLS Secrets, per-cluster topology) is read at runtime by the bootstrap binary against the Kubernetes API.

To change anything after install, edit the file and restart:

```bash
ssh admin@nas
sudo vi /var/packages/seaweedfs/var/volume.yaml
sudo synopkg restart seaweedfs
sudo tail -f /var/packages/seaweedfs/var/log/weed.log
```

The same edits the wizard would make are reachable here. There is no DSM-specific config layer above this file.

## Field reference

```yaml
kube:
  apiserver: https://kubernetes.example.com:6443
  # Path to a file containing the SA bearer token (no embedded JWT in YAML).
  tokenFile: /var/packages/seaweedfs/var/kube/token
  # Apiserver CA. Leave the file empty (zero bytes) to use the system CA store.
  caFile:    /var/packages/seaweedfs/var/kube/ca.crt
  # Last-resort override for self-signed clusters under test.
  insecureSkipTLSVerify: false
  namespace: seaweedfs
  # metadata.name of the Seaweed CR managed by seaweedfs-operator.
  seaweedName: seaweedfs
  # Without the operator: the master Service whose Endpoints list the
  # masters. Wins over seaweedName when both are set.
  # masterService: seaweedfs-master
  # masterPort: 9333          # master HTTP port for masterService
  # masterAddressMode: dns    # dns (default): weed resolves the Service name,
  #                           # so the NAS must resolve cluster DNS;
  #                           # endpoints: pod IPs captured at package start,
  #                           # stale once a master pod moves

volume:
  # On-disk directory `weed volume` writes data files into. The @ prefix
  # keeps DSM from managing it as a shared folder.
  dir: /volume1/@seaweedfs
  # Address advertised to masters. Must be reachable from every other
  # SeaweedFS node (master, filer, S3 gateway, peer volume).
  ip: 192.0.2.10
  publicUrl: 192.0.2.10:8080
  port: 8080
  grpcPort: 18080
  dataCenter: synology
  rack: nas-1
  # Maximum volume slot count for placement. Sized for DAS-class capacity.
  max: 1000
  diskType: hdd      # hdd | ssd | "" (omit)
  index: ""          # empty | leveldb | leveldbMedium | leveldbLarge
  # Verbatim flags appended to `weed volume`. Use sparingly; the
  # bootstrap does not validate them.
  extraFlags: []
  # Several servers sharing dir: server i serves port+i and grpcPort+i
  # from <dir>/v<i>, max applies per server.
  instances: 1
  # Or one server per directory, replacing dir and instances (see the
  # README's "One server per DSM volume"):
  # metricsPort: 9325
  # servers:
  #   - dir: /volume1/@seaweedfs
  #     port: 8080
  #     grpcPort: 18080
  #     memoryMB: 3072        # omit for automatic sizing
  #   - dir: /volume2/@seaweedfs

mtls:
  # Name of a Kubernetes Secret in `kube.namespace` containing
  # tls.crt / tls.key / ca.crt. Read at every start. Empty string
  # disables mTLS.
  secretName: ""

# Optional: run weed from an OCI image instead of the bundled binary
# (see the README's "Running weed from an OCI image").
weed:
  image: ""
  digest: ""
  plainHTTP: false
  binaries: []          # default: [/usr/bin/weed]
  cacheDir: /var/packages/seaweedfs/var/oci
```

To rewrite `volume.servers` without an editor, use the bootstrap:
`synology-volume-bootstrap --config <volume.yaml> --set-servers "/volume1/@seaweedfs=3072,/volume2/@seaweedfs=auto"`,
then run `register-service.sh`.

## Updating the bearer token

The kube token is a separate file so it can be rotated without touching `volume.yaml`:

```bash
NEW=$(kubectl -n seaweedfs create token synology-volume-server --duration=8760h)
ssh admin@nas "echo '$NEW' | sudo install -m 0600 -o sc-seaweedfs /dev/stdin /var/packages/seaweedfs/var/kube/token"
ssh admin@nas sudo synopkg restart seaweedfs
```

## Updating mTLS material

The bootstrap re-pulls the named Secret on every start, so updating mTLS is operator-side only:

```bash
kubectl -n seaweedfs apply -f new-mtls-secret.yaml
ssh admin@nas sudo synopkg restart seaweedfs
```

## Inspecting the rendered argv

```bash
ssh admin@nas \
  sudo -u sc-seaweedfs /var/packages/seaweedfs/target/bin/synology-volume-bootstrap \
    --config /var/packages/seaweedfs/var/volume.yaml \
    --tls-dir /var/packages/seaweedfs/var/tls \
    --print-only
```

This is what `weed volume` would be exec'd with on the next start. Compare against the in-cluster volume StatefulSet's args (`kubectl get sts <seaweed>-volume -n seaweedfs -o jsonpath='{.spec.template.spec.containers[0].args}' | jq`) to spot drift; the only legitimate differences should be `-ip`, `-publicUrl`, `-dataCenter`, `-rack`, `-dir`.

## Uninstalling

```bash
sudo synopkg uninstall seaweedfs
```

Server directories and their data are kept. So are `volume.yaml` and the token and CA under `/var/packages/seaweedfs/var/kube/` (the package state lives in `/volumeN/@appdata/seaweedfs`), so a reinstall picks them up again; delete them by hand for a clean slate.
