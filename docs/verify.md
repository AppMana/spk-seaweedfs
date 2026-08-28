# Verifying the install

End-to-end checks for a fresh Synology join, in dependency order.

## 1. Bootstrap dry-run

Confirm the bootstrap can talk to the apiserver and produces sensible argv before the daemon starts:

```bash
ssh admin@nas \
  sudo -u sc-seaweedfs /var/packages/seaweedfs/target/bin/synology-volume-bootstrap \
    --config /var/packages/seaweedfs/var/volume.yaml \
    --tls-dir /var/packages/seaweedfs/var/tls \
    --print-only
```

You should see one flag per line: `-mserver=...`, `-ip=...`, `-publicUrl=...`, `-dataCenter=...`, `-rack=...`, `-dir=...`, etc. Errors here are usually:

- `Forbidden`: token RBAC. Check `kube/rbac.yaml` is applied in the right namespace.
- `connection refused`: API server URL not reachable from the Synology. Test with `curl -k ${apiserver}/healthz`.
- `Seaweed ... not found`: `seaweedName` or `namespace` mismatch with the operator's CR.

## 2. Service starts

```bash
ssh admin@nas sudo synopkg start seaweedfs
ssh admin@nas sudo synopkg status seaweedfs   # → "start"
ssh admin@nas sudo tail -n 50 /var/packages/seaweedfs/var/log/weed.log
```

The log should show the bootstrap's "wrote N argv lines to ..." line, then `weed volume`'s startup banner, then a `master <addr> connected` heartbeat.

## 3. Masters see the new node

From any cluster pod with `weed shell`:

```bash
weed shell -master <master>:9333 <<EOF
volume.list
EOF
```

Look for an entry with `DataCenter:synology Rack:<your-rack>` and `PublicUrl:<your-synology-ip>:8080`. The `Volumes:` count starts at 0 because the cluster hasn't placed any volumes there yet.

## 4. Data plane works

Force a write to the new node by setting placement constraints:

```bash
weed shell -master <master>:9333 <<EOF
volume.grow -count 1 -dataCenter synology
EOF
```

Then put a file through the filer (or S3 gateway):

```bash
echo "hello synology" > /tmp/hello.txt
curl -F "file=@/tmp/hello.txt" "http://<filer>:8888/test/hello.txt"
```

Confirm it landed on the Synology:

```bash
ssh admin@nas ls -la /volume1/seaweedfs/
# expect *.dat / *.idx files
```

Read it back from outside the synology to verify the public URL is routable:

```bash
curl "http://<filer>:8888/test/hello.txt"
# → "hello synology"
```

## 5. Reboot persistence

Restart the Synology, wait for DSM to come up, confirm the daemon rejoins:

```bash
ssh admin@nas sudo /sbin/shutdown -r +1
# wait, then:
ssh admin@nas sudo synopkg status seaweedfs    # → "start"
weed shell -master <master>:9333 <<<'volume.list'   # entry returns
```

## 6. CLI/UI symmetry

Edit `volume.yaml` over SSH, restart, then re-open the wizard via Package Center → Configure. The wizard prefills should reflect the SSH edits (because both ultimately read the same `volume.yaml`). This is the homogeneity check: if the wizard ever shows stale values, it means a wizard hook is caching state outside `volume.yaml`, which would be a bug.

```bash
ssh admin@nas \
  sudo sed -i 's/^\(  max:\) .*/\1 2000/' /var/packages/seaweedfs/var/volume.yaml
ssh admin@nas sudo synopkg restart seaweedfs
# Then re-open the package's wizard via DSM Package Center; "Maximum
# volume count" should read 2000.
```

## 7. Token rotation

```bash
NEW=$(kubectl -n seaweedfs create token synology-volume-server --duration=24h)
ssh admin@nas "echo '$NEW' | sudo install -m 0600 -o sc-seaweedfs /dev/stdin /var/packages/seaweedfs/var/kube/token"
ssh admin@nas sudo synopkg restart seaweedfs
ssh admin@nas sudo grep -E 'rendered|Forbidden|wrote' /var/packages/seaweedfs/var/log/weed.log | tail -5
```

## 8. Supervisor restart contract

Run the process-level supervisor test before packaging:

```bash
make test-supervisor
```

The test launches a fake `weed` that exits 137 once and then exits cleanly. It
requires `run.sh` to log the failure, restart the child, and remain compatible
with `set -e`. A direct `wait "$CHILD"` is incorrect because exit 137 would
terminate the shell before its restart branch executes.

A successful re-render confirms the bootstrap re-auth'd against the new token.
