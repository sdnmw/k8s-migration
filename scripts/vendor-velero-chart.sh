#!/bin/sh
set -eu

chart_url="https://github.com/vmware-tanzu/helm-charts/releases/download/velero-12.1.0/velero-12.1.0.tgz"
chart_sha256="cd23589ad1b2d25cdd3220f6866b3f6f4c5683c4c09494e76a14700b33f81f83"
destination="${1:-deploy/charts/velero-12.1.0.tgz}"
temporary_directory="$(mktemp -d)"
trap 'rm -rf "$temporary_directory"' EXIT INT TERM

curl -fsSL "$chart_url" -o "$temporary_directory/velero.tgz"
actual_sha256="$(shasum -a 256 "$temporary_directory/velero.tgz" | awk '{print $1}')"
if [ "$actual_sha256" != "$chart_sha256" ]; then
  echo "Velero chart checksum mismatch" >&2
  exit 1
fi
mkdir -p "$(dirname "$destination")"
install -m 0644 "$temporary_directory/velero.tgz" "$destination"

