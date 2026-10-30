# Kubernetes integration

The Synology package needs read access to the `Seaweed` resource (operator path) or the master `Service` and `Endpoints` (`kube.masterService` path), plus the optional mTLS `Secret`, in the cluster's namespace. It needs no write access; volume to master heartbeats use no Kubernetes auth.

## Apply RBAC

`rbac.yaml` assumes the cluster runs in namespace `seaweedfs`. Edit it if yours differs, then:

```bash
kubectl apply -f kube/rbac.yaml
```

If `mtls.secretName` is set in `volume.yaml`, also pin the Secret name into the `Role`:

```yaml
- apiGroups: [""]
  resources: ["secrets"]
  resourceNames: ["seaweedfs-volume-tls"]
  verbs: ["get"]
```

This narrows the grant to just that one Secret.

## Mint a long-lived token

DSM packages cannot rotate projected service-account tokens themselves, so issue a long-duration bearer token bound to the SA:

```bash
kubectl -n seaweedfs create token synology-volume-server --duration=8760h
```

Copy the value into the wizard's "Service-account bearer token" field, or write it to `/var/packages/seaweedfs/var/kube/token` with mode `0600` over SSH.

## Discover the API server URL

```bash
kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}'
```

If the cluster's apiserver is reachable from the Synology only by an IP that doesn't match the API server's TLS SAN (e.g. you reach it via a LAN IP but the cert was signed for a hostname), either fix the SAN or fetch the API server's CA and copy it to `/var/packages/seaweedfs/var/kube/ca.crt`:

```bash
kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
  | base64 -d > /tmp/ca.crt
scp /tmp/ca.crt admin@<synology>:/tmp/ca.crt
ssh admin@<synology> 'sudo install -m 0644 -o sc-seaweedfs /tmp/ca.crt /var/packages/seaweedfs/var/kube/ca.crt'
```

## Verifying the SA can read what it needs

```bash
KUBE_TOKEN=$(kubectl -n seaweedfs create token synology-volume-server --duration=1h)
APISERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')

curl -sk -H "Authorization: Bearer ${KUBE_TOKEN}" \
  "${APISERVER}/apis/seaweed.seaweedfs.com/v1/namespaces/seaweedfs/seaweeds/<seaweed-name>" \
  | jq '.spec.master.replicas, (.spec.master.grpcPort // 19333)'
```

A successful response confirms the token and RBAC are sufficient before the package starts. For the `kube.masterService` path, read `${APISERVER}/api/v1/namespaces/seaweedfs/endpoints/<master-service>` instead.
