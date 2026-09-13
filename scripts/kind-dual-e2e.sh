#!/usr/bin/env bash
set -euo pipefail

KIND_BIN=${KIND_BIN:-kind}
KUBECTL_BIN=${KUBECTL_BIN:-kubectl}
SOURCE_CLUSTER=${SOURCE_CLUSTER:-smc-source}
TARGET_CLUSTER=${TARGET_CLUSTER:-smc-target}
WORKLOAD_IMAGE=${TEST_KIND_WORKLOAD_IMAGE:-docker.io/library/redis:8-alpine}
WORKLOAD_PORT=${TEST_KIND_WORKLOAD_PORT:-6379}
KIND_NODE_IMAGE=${KIND_NODE_IMAGE:-kindest/node:v1.32.5@sha256:e3b2327e3a5ab8c76f5ece68936e4cafaa82edf58486b769727ab0b3b97a5b0d}
GO_BIN=${GO_BIN:-.cache/toolchains/go/bin/go}

if [[ ! -x $GO_BIN ]]; then
  GO_BIN=$(command -v go)
fi

command -v "$KIND_BIN" >/dev/null
command -v "$KUBECTL_BIN" >/dev/null
docker image inspect "$WORKLOAD_IMAGE" >/dev/null

e2e_tmp=$(mktemp -d)
cleanup() {
  if [[ ${KEEP_KIND_CLUSTERS:-0} != 1 ]]; then
    "$KIND_BIN" delete cluster --name "$SOURCE_CLUSTER" >/dev/null 2>&1 || true
    "$KIND_BIN" delete cluster --name "$TARGET_CLUSTER" >/dev/null 2>&1 || true
  fi
  rm -rf "$e2e_tmp"
}
trap cleanup EXIT

"$KIND_BIN" create cluster --name "$SOURCE_CLUSTER" --image "$KIND_NODE_IMAGE" --wait 180s
"$KIND_BIN" create cluster --name "$TARGET_CLUSTER" --image "$KIND_NODE_IMAGE" --wait 180s
"$KIND_BIN" load docker-image "$WORKLOAD_IMAGE" --name "$SOURCE_CLUSTER"
"$KIND_BIN" load docker-image "$WORKLOAD_IMAGE" --name "$TARGET_CLUSTER"
"$KIND_BIN" get kubeconfig --name "$SOURCE_CLUSTER" > "$e2e_tmp/source.yaml"
"$KIND_BIN" get kubeconfig --name "$TARGET_CLUSTER" > "$e2e_tmp/target.yaml"
chmod 0600 "$e2e_tmp/source.yaml" "$e2e_tmp/target.yaml"

TEST_KIND_SOURCE_KUBECONFIG="$e2e_tmp/source.yaml" \
TEST_KIND_TARGET_KUBECONFIG="$e2e_tmp/target.yaml" \
TEST_KIND_WORKLOAD_IMAGE="$WORKLOAD_IMAGE" \
TEST_KIND_WORKLOAD_PORT="$WORKLOAD_PORT" \
  "$GO_BIN" test -v ./internal/acceptance -run '^TestKindDualClusterControlPath$' -count=1 -timeout=5m
