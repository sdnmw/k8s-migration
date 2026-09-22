#!/bin/sh
set -eu

# Builds the accepted one-time, linux/amd64 offline delivery. Every upstream
# payload is resolved from an official image through the configured DaoCloud
# mirror and re-exported as a local OCI layout. Evidence marker files are
# explicit about the intentionally relaxed signing and scan policy.

root_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${1:-"$root_dir/output/sks-migration-center-0.4.0-amd64"}
staging_dir="$output_dir/staging"
archive="$output_dir/sks-migration-center-0.4.0-linux-amd64.tar.gz"
go_bin=${GO_BIN:-"$root_dir/.cache/toolchains/go/bin/go"}
reuse_official_layouts_dir=${REUSE_OFFICIAL_LAYOUTS_DIR:-}

if [ ! -x "$go_bin" ]; then
  echo "Go toolchain is unavailable: $go_bin" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker with buildx is required" >&2
  exit 1
fi
if [ -e "$staging_dir" ] || [ -e "$archive" ] || [ -e "$archive.sha256" ]; then
  echo "Refusing to overwrite existing output: $output_dir" >&2
  exit 1
fi

mkdir -p "$staging_dir/images" "$staging_dir/sbom" "$staging_dir/signatures" \
  "$staging_dir/scans" "$staging_dir/bin" "$staging_dir/charts" "$staging_dir/policies"

copy_layout() {
  source_dir=$1
  name=$2
  mkdir -p "$staging_dir/images/$name"
  cp -R "$source_dir"/. "$staging_dir/images/$name/"
}

extract_layout() {
  archive_path=$1
  name=$2
  mkdir -p "$staging_dir/images/$name"
  tar -xf "$archive_path" -C "$staging_dir/images/$name"
}

build_upstream() {
  name=$1
  source_image=$2
  oci_tar="$output_dir/$name.oci.tar"
  docker buildx build --platform linux/amd64 \
    --build-arg "SOURCE_IMAGE=$source_image" \
    -f "$root_dir/build/package/upstream.Dockerfile" \
    --output "type=oci,dest=$oci_tar" "$root_dir"
  extract_layout "$oci_tar" "$name"
}

build_platform() {
  name=$1
  dockerfile=$2
  oci_tar="$output_dir/$name.oci.tar"
  docker buildx build --platform linux/amd64 \
    -f "$root_dir/$dockerfile" \
    --output "type=oci,dest=$oci_tar" "$root_dir"
  extract_layout "$oci_tar" "$name"
}

build_precompiled_platform() {
  name=$1
  dockerfile=$2
  prebuilt_context=$3
  oci_tar="$output_dir/$name.oci.tar"
  docker buildx build --platform linux/amd64 \
    --build-context "prebuilt=$prebuilt_context" \
    -f "$root_dir/$dockerfile" \
    --output "type=oci,dest=$oci_tar" "$root_dir"
  extract_layout "$oci_tar" "$name"
}

export_platform_image() {
  name=$1
  source_image=$2
  oci_tar="$output_dir/$name.oci.tar"
  docker --context desktop-linux buildx build --builder desktop-linux --pull=false \
    --platform linux/amd64 \
    --build-arg "SOURCE_IMAGE=$source_image" \
    -f "$root_dir/build/package/upstream.Dockerfile" \
    --output "type=oci,dest=$oci_tar" "$root_dir"
  extract_layout "$oci_tar" "$name"
}

write_evidence() {
  name=$1
  digest=$2
  printf '%s\n' "{\"spdxVersion\":\"SPDX-2.3\",\"name\":\"$name-official-image-lab-delivery\",\"comment\":\"One-time migration delivery; package inventory only.\"}" >"$staging_dir/sbom/$name.spdx.json"
  printf '%s\n' "{\"status\":\"NOT_REQUIRED_FOR_ONE_TIME_TOOL\",\"digest\":\"$digest\"}" >"$staging_dir/signatures/$name.bundle.json"
  printf '%s\n' '{"status":"NOT_BLOCKING","scope":"one-time isolated migration tool using official images"}' >"$staging_dir/scans/$name.json"
}

layout_digest() {
  layout=$1
  sed -n 's/.*"digest":"\(sha256:[a-f0-9]*\)".*/\1/p' "$layout/index.json" | head -1
}

platform_cache="$root_dir/.cache/release-0.1.0/staging/images"
compose_cache="$root_dir/.cache/compose-e2e/staging/images"
if [ -n "${PLATFORM_API_IMAGE:-}" ] || [ -n "${PLATFORM_WORKER_IMAGE:-}" ] || [ -n "${PLATFORM_WEB_IMAGE:-}" ]; then
  : "${PLATFORM_API_IMAGE:?PLATFORM_API_IMAGE is required when exporting existing platform images}"
  : "${PLATFORM_WORKER_IMAGE:?PLATFORM_WORKER_IMAGE is required when exporting existing platform images}"
  : "${PLATFORM_WEB_IMAGE:?PLATFORM_WEB_IMAGE is required when exporting existing platform images}"
  export_platform_image api "$PLATFORM_API_IMAGE"
  export_platform_image worker "$PLATFORM_WORKER_IMAGE"
  export_platform_image web "$PLATFORM_WEB_IMAGE"
else
  case "$(uname -m)" in
    x86_64|amd64)
      build_platform api build/package/api.Dockerfile
      build_platform worker build/package/worker.Dockerfile
      build_platform web build/package/web.Dockerfile
      ;;
    *)
      # Compiling Go under QEMU is both slow and prone to emulator faults on
      # Apple Silicon. Cross-compile the static linux/amd64 binaries natively,
      # build architecture-independent web assets natively, then use buildx
      # only to assemble the pinned AMD64 runtime layers.
      prebuilt_dir="$output_dir/prebuilt"
      mkdir -p "$prebuilt_dir/api" "$prebuilt_dir/worker" "$prebuilt_dir/web/dist"
      (cd "$root_dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags="-s -w" -o "$prebuilt_dir/api/server" ./cmd/server)
      (cd "$root_dir" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -ldflags="-s -w" -o "$prebuilt_dir/worker/worker" ./cmd/worker)
      (cd "$root_dir/web" && npm ci && npm run build)
      cp -R "$root_dir/web/dist"/. "$prebuilt_dir/web/dist/"
      build_precompiled_platform api build/package/api-prebuilt.Dockerfile "$prebuilt_dir/api"
      build_precompiled_platform worker build/package/worker-prebuilt-release.Dockerfile "$prebuilt_dir/worker"
      build_precompiled_platform web build/package/web-prebuilt.Dockerfile "$prebuilt_dir/web"
      ;;
  esac
fi

if [ -n "$reuse_official_layouts_dir" ]; then
  for name in postgres minio velero velero-plugin-for-aws nfsplugin csi-provisioner csi-resizer csi-node-driver-registrar livenessprobe busybox kompose kopia migration-helper; do
    copy_layout "$reuse_official_layouts_dir/$name" "$name"
  done
else
  copy_layout "$platform_cache/postgres" postgres
  copy_layout "$compose_cache/kompose" kompose
  copy_layout "$compose_cache/kopia" kopia

  build_upstream minio "m.daocloud.io/docker.io/minio/minio@sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e"
  build_upstream velero "m.daocloud.io/docker.io/velero/velero@sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90"
  build_upstream velero-plugin-for-aws "m.daocloud.io/docker.io/velero/velero-plugin-for-aws@sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278"
  build_upstream nfsplugin "m.daocloud.io/registry.k8s.io/sig-storage/nfsplugin@sha256:1eb5a85180a4ad0193a31d319b163f35c8c1857794ebaac71d8abcdd5a0516d3"
  build_upstream csi-provisioner "m.daocloud.io/registry.k8s.io/sig-storage/csi-provisioner@sha256:a4b0b1a37605b7b04a293e136edf7006ec1786a8eb3f4e5a945f81d667dcc371"
  build_upstream csi-resizer "m.daocloud.io/registry.k8s.io/sig-storage/csi-resizer@sha256:a2d40c1c3ccb0c48b467125a6652c4dd5dcbf0d295641c9989581cfc690f6cf3"
  build_upstream csi-node-driver-registrar "m.daocloud.io/registry.k8s.io/sig-storage/csi-node-driver-registrar@sha256:f9de845b170155199f2a2a3f9531cf13d78e31235e9db6b6582a8b0db0a50dad"
  build_upstream livenessprobe "m.daocloud.io/registry.k8s.io/sig-storage/livenessprobe@sha256:06da0d5b8908072f2e4522692aee8dc119fba7247a9658497e1153992cd777e9"
  build_upstream busybox "m.daocloud.io/docker.io/library/busybox@sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662"
  copy_layout "$staging_dir/images/busybox" migration-helper
fi

for name in api worker web postgres minio velero velero-plugin-for-aws nfsplugin csi-provisioner csi-resizer csi-node-driver-registrar livenessprobe busybox kompose kopia migration-helper; do
  digest=$(layout_digest "$staging_dir/images/$name")
  if [ -z "$digest" ]; then
    echo "Could not resolve OCI root digest for $name" >&2
    exit 1
  fi
  write_evidence "$name" "$digest"
done

api_digest=$(layout_digest "$staging_dir/images/api")
worker_digest=$(layout_digest "$staging_dir/images/worker")
web_digest=$(layout_digest "$staging_dir/images/web")
postgres_digest=$(layout_digest "$staging_dir/images/postgres")
minio_digest=$(layout_digest "$staging_dir/images/minio")
velero_digest=$(layout_digest "$staging_dir/images/velero")
aws_digest=$(layout_digest "$staging_dir/images/velero-plugin-for-aws")
nfs_digest=$(layout_digest "$staging_dir/images/nfsplugin")
provisioner_digest=$(layout_digest "$staging_dir/images/csi-provisioner")
resizer_digest=$(layout_digest "$staging_dir/images/csi-resizer")
registrar_digest=$(layout_digest "$staging_dir/images/csi-node-driver-registrar")
liveness_digest=$(layout_digest "$staging_dir/images/livenessprobe")
busybox_digest=$(layout_digest "$staging_dir/images/busybox")
kompose_digest=$(layout_digest "$staging_dir/images/kompose")
kopia_digest=$(layout_digest "$staging_dir/images/kopia")
helper_digest=$(layout_digest "$staging_dir/images/migration-helper")

sed \
  -e "s|@API_DIGEST@|$api_digest|g" \
  -e "s|@WORKER_DIGEST@|$worker_digest|g" \
  -e "s|@WEB_DIGEST@|$web_digest|g" \
  -e "s|@POSTGRES_DIGEST@|$postgres_digest|g" \
  -e "s|@MINIO_DIGEST@|$minio_digest|g" \
  -e "s|@VELERO_DIGEST@|$velero_digest|g" \
  -e "s|@AWS_DIGEST@|$aws_digest|g" \
  -e "s|@NFS_DIGEST@|$nfs_digest|g" \
  -e "s|@PROVISIONER_DIGEST@|$provisioner_digest|g" \
  -e "s|@RESIZER_DIGEST@|$resizer_digest|g" \
  -e "s|@REGISTRAR_DIGEST@|$registrar_digest|g" \
  -e "s|@LIVENESS_DIGEST@|$liveness_digest|g" \
  -e "s|@BUSYBOX_DIGEST@|$busybox_digest|g" \
  -e "s|@KOMPOSE_DIGEST@|$kompose_digest|g" \
  -e "s|@KOPIA_DIGEST@|$kopia_digest|g" \
  -e "s|@HELPER_DIGEST@|$helper_digest|g" \
  "$root_dir/deploy/offline/images.lab-amd64.lock.yaml.tmpl" >"$staging_dir/images.lock.yaml"

cp -R "$root_dir/deploy/charts"/. "$staging_dir/charts/"
cp "$root_dir/build/minio/source.lock.yaml" "$staging_dir/policies/minio-source.lock.yaml"
cp "$root_dir/build/kompose/source.lock.yaml" "$staging_dir/policies/kompose-source.lock.yaml"
cp "$root_dir/build/kopia/source.lock.yaml" "$staging_dir/policies/kopia-source.lock.yaml"
cp "$root_dir/deploy/offline/README.md" "$staging_dir/README.md"
cp "$root_dir/docs/operations/migration-failure-lessons.md" "$staging_dir/MIGRATION-TROUBLESHOOTING.md"
cp "$root_dir/MIGRATION_USER_GUIDE.md" "$staging_dir/MIGRATION_USER_GUIDE.md"
mkdir -p "$staging_dir/docs/images/migration-user-guide"
cp -R "$root_dir/docs/images/migration-user-guide"/. "$staging_dir/docs/images/migration-user-guide/"
cp "$root_dir/NFS_MIGRATION_GUIDE.md" "$staging_dir/NFS_MIGRATION_GUIDE.md"
mkdir -p "$staging_dir/docs/images/nfs-migration"
cp -R "$root_dir/docs/images/nfs-migration"/. "$staging_dir/docs/images/nfs-migration/"
cp "$root_dir/deploy/offline/components.yaml" "$staging_dir/components.yaml"
cp "$root_dir/deploy/offline/deploy.sh" "$staging_dir/deploy.sh"
chmod 0755 "$staging_dir/deploy.sh"

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$go_bin" build -trimpath -o "$staging_dir/bin/offline" "$root_dir/cmd/offline"
"$go_bin" build -trimpath -o "$output_dir/offline-pack" "$root_dir/cmd/offline"
"$output_dir/offline-pack" pack --source "$staging_dir" --output "$archive"

verify_dir="$output_dir/verified"
mkdir -p "$verify_dir"
tar -xzf "$archive" -C "$verify_dir"
"$output_dir/offline-pack" verify --directory "$verify_dir"
rm "$output_dir/offline-pack"
(cd "$output_dir" && shasum -a 256 "$(basename "$archive")" >"$(basename "$archive").sha256")
echo "Lab offline bundle ready: $archive"
