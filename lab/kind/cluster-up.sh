#!/usr/bin/env bash
# kind cluster `swlab` + seaweedfs-operator + Synology discovery RBAC.
#
# Produces in run/:
#   cluster.env   APISERVER / CONTROL_PLANE_IP / SAN_OK
#   ca.crt        cluster CA for the SPK's kube client
#   token         long-lived SA token for synology-volume-server
set -euo pipefail

KIND="${KIND:-$HOME/go/bin/kind}"
CLUSTER=swlab
LAB_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RUN_DIR="$LAB_DIR/kind/run"
OPERATOR_DIR="$LAB_DIR/.cache/seaweedfs-operator"
REGISTRY_IP="${REGISTRY_IP:-172.21.240.10}"
mkdir -p "$RUN_DIR"

# kind on this host needs more inotify instances (prior-lab lesson).
if [ "$(sysctl -n fs.inotify.max_user_instances)" -lt 1024 ]; then
  sudo sysctl -w fs.inotify.max_user_instances=1024
fi

if ! "$KIND" get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  "$KIND" create cluster --name "$CLUSTER" --config "$LAB_DIR/kind/kind-config.yaml" --wait 120s
else
  echo "cluster $CLUSTER already exists"
fi
kubectl config use-context "kind-$CLUSTER" >/dev/null

# Plain-HTTP pull config for the lab registry on every node.
for node in $("$KIND" get nodes --name "$CLUSTER"); do
  docker exec "$node" mkdir -p "/etc/containerd/certs.d/${REGISTRY_IP}:5000"
  docker exec -i "$node" tee "/etc/containerd/certs.d/${REGISTRY_IP}:5000/hosts.toml" >/dev/null <<EOF
server = "http://${REGISTRY_IP}:5000"
[host."http://${REGISTRY_IP}:5000"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
EOF
done

# seaweedfs-operator (CRDs + manager), pinned by the checkout in .cache.
if [ ! -d "$OPERATOR_DIR" ]; then
  git clone --depth 1 https://github.com/seaweedfs/seaweedfs-operator "$OPERATOR_DIR"
fi
kubectl apply --server-side -k "$OPERATOR_DIR/config/crd" >/dev/null
kubectl apply --server-side -k "$OPERATOR_DIR/config/default" >/dev/null
# The repo's kustomization pins ghcr.io/seaweedfs/seaweedfs-operator:v0.0.1,
# which is not published; latest is.
kubectl -n seaweedfs-operator-system set image deploy/seaweedfs-operator-controller-manager \
  manager=ghcr.io/seaweedfs/seaweedfs-operator:latest >/dev/null
kubectl -n seaweedfs-operator-system rollout status deploy --timeout=180s 2>/dev/null || \
  kubectl get deploy -A | grep -i operator

# Namespace + discovery RBAC + long-lived token for the Synology.
kubectl create namespace seaweedfs --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl apply -f "$LAB_DIR/../kube/rbac.yaml" >/dev/null
kubectl -n seaweedfs apply -f - >/dev/null <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: synology-volume-server-token
  annotations:
    kubernetes.io/service-account.name: synology-volume-server
type: kubernetes.io/service-account-token
EOF
for i in $(seq 1 20); do
  TOKEN=$(kubectl -n seaweedfs get secret synology-volume-server-token -o jsonpath='{.data.token}' 2>/dev/null | base64 -d)
  [ -n "$TOKEN" ] && break
  sleep 1
done
[ -n "$TOKEN" ] || { echo "SA token never populated" >&2; exit 1; }
printf '%s' "$TOKEN" > "$RUN_DIR/token"
chmod 600 "$RUN_DIR/token"

kubectl config view --raw --minify -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
  | base64 -d > "$RUN_DIR/ca.crt"

CONTROL_PLANE_IP=$(docker inspect "${CLUSTER}-control-plane" \
  -f '{{.NetworkSettings.Networks.kind.IPAddress}}')

# The SPK talks to https://<node-ip>:6443 across the lab LAN; verify the
# apiserver cert covers the node IP so insecureSkipTLSVerify can stay off.
SAN_OK=false
if echo | timeout 5 openssl s_client -connect "${CONTROL_PLANE_IP}:6443" 2>/dev/null \
    | openssl x509 -noout -ext subjectAltName 2>/dev/null | grep -q "$CONTROL_PLANE_IP"; then
  SAN_OK=true
fi

{
  echo "CONTROL_PLANE_IP=$CONTROL_PLANE_IP"
  echo "APISERVER=https://${CONTROL_PLANE_IP}:6443"
  echo "SAN_OK=$SAN_OK"
} > "$RUN_DIR/cluster.env"
echo "cluster up: apiserver https://${CONTROL_PLANE_IP}:6443 (cert covers node IP: $SAN_OK)"
