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
  apiserver: https://api.k8s.appmana.com:6443
  # Path to a file containing the SA bearer token (no embedded JWT in YAML).
  tokenFile: /var/packages/seaweedfs/var/kube/token
  # Apiserver CA. Leave the file empty (zero bytes) to use the system CA store.
  caFile:    /var/packages/seaweedfs/var/kube/ca.crt
  # Last-resort override for self-signed clusters under test.
  insecureSkipTLSVerify: false
  namespace: seaweedfs
  # metadata.name of the Seaweed CR managed by seaweedfs-operator.
  seaweedName: appmana

volume:
  # On-disk directory `weed volume` writes data files into.
  dir: /volume1/seaweedfs
  # Address advertised to masters. Must be reachable from every other
  # SeaweedFS node (master, filer, S3 gateway, peer volume).
  ip: 10.2.0.73
  publicUrl: 10.2.0.73:8080
  port: 8080
  grpcPort: 18080
  dataCenter: synology
  rack: appmana-017-ds
  # Maximum volume slot count for placement. Sized for DAS-class capacity.
  max: 1000
  diskType: hdd      # hdd | ssd | "" (omit)
  index: ""          # empty | leveldb | leveldbMedium | leveldbLarge
  # Verbatim flags appended to `weed volume`. Use sparingly; the
  # bootstrap does not validate them.
  extraFlags: []

mtls:
  # Name of a Kubernetes Secret in `kube.namespace` containing
  # tls.crt / tls.key / ca.crt. Read at every start. Empty string
  # disables mTLS.
  secretName: ""
```

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

Data under `/volume1/<sharename>/` is preserved. Tokens and certs under `/var/packages/seaweedfs/var/kube/` are wiped along with the package; back them up first if you want to reinstall without re-minting.
