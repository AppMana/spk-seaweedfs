# Installing over SSH

Lets you stand the package up on a Synology that you reach by SSH only, and gives the same end-state as a Package Center install. Both paths converge on `/var/packages/seaweedfs/var/volume.yaml` and the `kube/` token directory.

## Prerequisites

- DSM 7.0 or later, x86_64.
- SSH enabled on the Synology with an account in the `administrators` group.
- `Trust Level: Any publisher` set under `Package Center → Settings → General` (one-time setup; only affects sideloading unsigned community packages).
- A Kubernetes service-account token minted via [`kube/rbac.yaml`](../kube/rbac.yaml) (see [`kube/README.md`](../kube/README.md)).

## 1. Copy the SPK to the Synology

From a workstation with the built SPK:

```bash
SPK=$(make show-spk | head -1)
scp "$SPK" admin@nas:/tmp/seaweedfs.spk
```

Or download directly on the NAS from a release artifact:

```bash
ssh admin@nas \
  curl -fL -o /tmp/seaweedfs.spk \
  https://github.com/appmana/spk-seaweedfs/releases/download/v4.23-1/seaweedfs_x86_64-dsm72_4.23-1.spk
```

## 2. Install

```bash
ssh admin@nas
sudo synopkg install /tmp/seaweedfs.spk
```

`synopkg install` runs the wizard's `preinst` / `postinst` non-interactively, but every wizard input (apiserver URL, token, namespace, etc.) is empty because there is no UI. The package will be installed but **not started**, because `volume.yaml` has empty placeholders. Configure it next.

## 3. Configure

Drop the bearer token (and CA, if your apiserver uses a private CA):

```bash
PKGVAR=/var/packages/seaweedfs/var
sudo install -d -m 0700 -o sc-seaweedfs $PKGVAR/kube
echo "$KUBE_TOKEN" | sudo install -m 0600 -o sc-seaweedfs /dev/stdin $PKGVAR/kube/token
sudo install -m 0644 -o sc-seaweedfs ca.crt $PKGVAR/kube/ca.crt   # optional
```

Write `volume.yaml`:

```bash
sudo tee $PKGVAR/volume.yaml >/dev/null <<'EOF'
kube:
  apiserver: https://api.k8s.appmana.com:6443
  tokenFile: /var/packages/seaweedfs/var/kube/token
  caFile:    /var/packages/seaweedfs/var/kube/ca.crt
  insecureSkipTLSVerify: false
  namespace: seaweedfs
  seaweedName: appmana
volume:
  dir: /volume1/seaweedfs
  ip: 10.2.0.73
  publicUrl: 10.2.0.73:8080
  port: 8080
  grpcPort: 18080
  dataCenter: synology
  rack: appmana-017-ds
  max: 1000
  diskType: hdd
  index: ""
  extraFlags: []
mtls:
  secretName: ""
EOF
sudo chown sc-seaweedfs:sc-seaweedfs $PKGVAR/volume.yaml
sudo chmod 0600 $PKGVAR/volume.yaml
```

Note: `volume.dir` is the **on-disk path** to the data directory. If you used the wizard, the package's `shares/seaweedfs` symlink points at the chosen shared folder; over SSH you can either:

- Point `volume.dir` directly at `/volume1/<sharename>/seaweedfs` (most common); or
- Pre-create the package's symlink: `sudo ln -s /volume1/seaweedfs $PKGVAR/shares/seaweedfs` and set `volume.dir: /var/packages/seaweedfs/var/shares/seaweedfs`.

## 4. Start

```bash
sudo synopkg start seaweedfs
sudo synopkg status seaweedfs
sudo tail -f /var/packages/seaweedfs/var/log/weed.log
```

The first lines of the log are from the bootstrap (`wrote N argv lines to ...`), then `weed volume` takes over and prints heartbeat/registration output.

## 5. Sanity check the join

From any pod inside the cluster (or over `kubectl exec` into a master):

```bash
weed shell -master <master>:9333 <<<'volume.list'
```

Look for a node entry where `DataCenter:synology Rack:appmana-017-ds` and `PublicUrl:10.2.0.73:8080`. That is the Synology.

## Common issues

- **"Forbidden: cannot get resource seaweeds"**: RBAC didn't apply or the token's namespace differs. Re-run `kubectl -n seaweedfs create token synology-volume-server`.
- **"x509: certificate signed by unknown authority"**: drop the apiserver CA at `kube/ca.crt`, or temporarily set `kube.insecureSkipTLSVerify: true` in `volume.yaml` while debugging.
- **`weed volume` registers but masters can't dial back**: `volume.ip` / `publicUrl` advertised an IP the cluster cannot route. Calico BGP must consider the Synology's LAN IP reachable; verify with `kubectl run -it --rm netcheck --image=busybox -- sh` and `wget http://<synology>:8080/status` from inside.
- **Volume daemon dies immediately**: read `bootstrap` errors at the top of `weed.log`; usually a `volume.yaml` field validation failure.
