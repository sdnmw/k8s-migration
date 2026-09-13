# Upstream provenance

- Project: Kubernetes CSI NFS Driver
- Source: https://github.com/kubernetes-csi/csi-driver-nfs
- Release: `v4.13.4`
- Chart source: `charts/latest/csi-driver-nfs`
- License: Apache-2.0

This offline chart preserves the upstream controller/node/RBAC architecture but reduces the value surface to the migration system's supported configuration. Images are fixed by multi-platform digest in `values.yaml` and `deploy/offline/images.lock.yaml.tmpl`; an offline installer replaces only the repository with the customer Harbor location.
