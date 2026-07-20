#!/usr/bin/env bash
# Lab registry + fork image build.
#
# Runs a registry:2 at a static IP on the shared `kind` docker network
# so kind nodes and the DSM VM can pull from it over plain HTTP, and
# publishes the AppMana SeaweedFS fork image into it, built from the
# LOCAL checkout via docker/Dockerfile.local (Dockerfile.go_build clones
# from GitHub and cannot build uncommitted/local state).
#
# Pushing goes through the 127.0.0.1 host port: docker treats localhost
# registries as insecure by default, so no daemon.json changes (which
# would restart dockerd under the other live kind clusters) are needed.
set -euo pipefail

REGISTRY_NAME=swlab-registry
REGISTRY_IP="${REGISTRY_IP:-172.21.240.10}"
REGISTRY_HOST_PORT="${REGISTRY_HOST_PORT:-5002}"
FORK_DIR="${FORK_DIR:-$HOME/Documents/forks-seaweedfs}"
FORK_TAG=seaweedfs:fork-large-disk
RUN_DIR="$(cd "$(dirname "$0")" && pwd)/run"
mkdir -p "$RUN_DIR"

if ! docker ps --format '{{.Names}}' | grep -qx "$REGISTRY_NAME"; then
  docker rm -f "$REGISTRY_NAME" >/dev/null 2>&1 || true
  docker run -d --name "$REGISTRY_NAME" --restart=unless-stopped \
    --network kind --ip "$REGISTRY_IP" \
    -p "127.0.0.1:${REGISTRY_HOST_PORT}:5000" \
    registry:2 >/dev/null
  echo "registry up at ${REGISTRY_IP}:5000 (host 127.0.0.1:${REGISTRY_HOST_PORT})"
else
  echo "registry already running"
fi

# Build the fork's weed with the large-disk tag, exactly like the fork
# CI's large_disk variant (TAGS=5BytesOffset), then package it with the
# stock Dockerfile.local.
echo "building weed from ${FORK_DIR} ($(git -C "$FORK_DIR" rev-parse --short HEAD), $(git -C "$FORK_DIR" branch --show-current))"
COMMIT=$(git -C "$FORK_DIR" rev-parse --short HEAD)
(
  cd "$FORK_DIR/weed"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -tags 5BytesOffset \
    -ldflags "-extldflags -static -X github.com/seaweedfs/seaweedfs/weed/util/version.COMMIT=${COMMIT}" \
    -o "$FORK_DIR/docker/weed" .
)
docker build -q -t "$FORK_TAG" -f "$FORK_DIR/docker/Dockerfile.local" "$FORK_DIR/docker" >/dev/null
rm -f "$FORK_DIR/docker/weed"

docker tag "$FORK_TAG" "127.0.0.1:${REGISTRY_HOST_PORT}/${FORK_TAG}"
docker push -q "127.0.0.1:${REGISTRY_HOST_PORT}/${FORK_TAG}"

DIGEST=$(docker inspect "127.0.0.1:${REGISTRY_HOST_PORT}/${FORK_TAG}" \
  --format '{{range .RepoDigests}}{{.}}{{"\n"}}{{end}}' | grep "^127.0.0.1:${REGISTRY_HOST_PORT}/" | cut -d@ -f2)
{
  echo "REGISTRY_IP=$REGISTRY_IP"
  echo "FORK_IMAGE=${REGISTRY_IP}:5000/${FORK_TAG}"
  echo "FORK_DIGEST=$DIGEST"
  echo "FORK_COMMIT=$COMMIT"
} > "$RUN_DIR/registry.env"
echo "pushed ${REGISTRY_IP}:5000/${FORK_TAG} (${DIGEST:-digest-unknown}, commit ${COMMIT})"
