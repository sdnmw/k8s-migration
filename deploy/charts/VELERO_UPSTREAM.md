# Velero upstream chart lock

- Chart: `vmware-tanzu/velero`
- Chart version: `12.1.0`
- App version: `1.18.1`
- Release artifact: `velero-12.1.0.tgz`
- Artifact SHA-256: `cd23589ad1b2d25cdd3220f6866b3f6f4c5683c4c09494e76a14700b33f81f83`
- AWS plugin: `velero/velero-plugin-for-aws:v1.14.0`

The release pipeline runs `make vendor-velero-chart` while it still has
internet access, then copies the verified chart and pinned OCI image layouts
into the offline bundle. Target clusters never download a chart or image from
the internet. Runtime installation uses the embedded Helm Go SDK.

