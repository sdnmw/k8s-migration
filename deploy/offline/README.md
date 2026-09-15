# 离线交付目录

正式发行物是一个包含 OCI Image Layout 的确定性 `tar.gz`，不依赖目标环境访问公网，也不要求目标主机安装 Docker、Skopeo、Crane 或 Helm CLI。

## v0.2.0 AMD64 发行包

GitHub Release 中的 `sks-migration-center-0.2.0-linux-amd64.tar.gz` 是一次性迁移场景的完整离线包。归档包含 16 个 `linux/amd64` OCI Image Layout、平台 Helm Chart、静态 Linux AMD64 安装器、镜像锁和校验清单。部署端不需要访问公网，也不要求预装 Docker、Skopeo、Crane 或 Helm CLI。

下载后先校验同一 Release 中的 SHA-256 sidecar：

```bash
shasum -a 256 -c sks-migration-center-0.2.0-linux-amd64.tar.gz.sha256
tar -xzf sks-migration-center-0.2.0-linux-amd64.tar.gz
```

## 发行侧组装

1. 按 `components.yaml` 构建或获取全部必需镜像，并固定为 `linux/amd64`。
2. 将每个镜像按已解析的 source digest 导出到 `images/<name>/` OCI Image Layout。
3. 为每个镜像生成 SPDX JSON 清单、签名状态和扫描状态证据。按一次性迁移工具边界，v0.2.0 使用官方镜像并记录接受状态，不设置漏洞阻断门禁。
4. 用真实值渲染 `images.lock.yaml.tmpl` 为 `images.lock.yaml`，并为后续窗口加入的镜像补齐条目。
5. 将 `deploy/charts/`、CRD、安装配置和发行说明复制进 staging 目录后打包：

```bash
go run ./cmd/offline pack --source ./staging --output sks-migration-center-offline.tar.gz
```

打包器拒绝未固定 digest、缺失 OCI layout、缺失 SBOM/签名、目录逃逸和符号链接；归档时间戳固定，重复构建可复现。它同时生成 `bundle-manifest.json` 与 `SHA256SUMS`。

最终归档使用离线保管的 Ed25519 私钥签名；验收端必须提供独立获得的可信公钥，不能只信任签名 bundle 内嵌的公钥：

```bash
openssl genpkey -algorithm ED25519 -out release-private.pem
openssl pkey -in release-private.pem -pubout -out release-public.pem
offline sign --archive sks-migration-center-offline.tar.gz \
  --private-key-file release-private.pem \
  --output sks-migration-center-offline.tar.gz.sig.json
offline verify-signature --archive sks-migration-center-offline.tar.gz \
  --signature sks-migration-center-offline.tar.gz.sig.json \
  --public-key-file release-public.pem
```

签名覆盖压缩归档的 SHA-256；签名 bundle 记录算法、key ID、归档 digest、公开密钥和 Ed25519 签名。私钥不会写入归档或日志。

MinIO 使用官方 `docker.io/minio/minio:RELEASE.2025-09-07T16-13-09Z`，离线复制时必须固定 `build/minio/source.lock.yaml` 中的 multi-platform index digest。该版本的已知风险已按一次性迁移工具边界显式接受，状态为 `ACCEPTED_RISK`；不得使用 `latest`、省略 MinIO 条目或改用未经记录的第三方镜像。

NFS CSI 使用 Kubernetes SIG Storage 官方 `csi-driver-nfs` 4.13.4 Chart 语义。`nfsplugin`、provisioner、resizer、node registrar、liveness probe 和读写探测 BusyBox 均以 multi-platform index digest 固定；隔离区导入 Harbor 后只改写 repository，不改变内容 digest。

Velero 使用官方 Chart `12.1.0`（应用版本 `1.18.1`）及官方 AWS 插件 `1.14.0`。发行侧先通过固定 SHA-256 获取 Chart，再把 Chart、Velero 和插件 OCI layout 一并封装；目标环境通过 Helm Go SDK 安装，不运行 Velero CLI，也不访问公网。

## 目标侧校验与导入 Harbor

在隔离区解压后先校验全部文件，再从本地 OCI layout 直接推送 Registry v2/Harbor：

```bash
offline verify --directory ./bundle
offline import --directory ./bundle \
  --endpoint https://harbor.example.com \
  --username-file /run/secrets/harbor/username \
  --password-file /run/secrets/harbor/password
```

导入器验证 lock digest、OCI descriptor digest/size 和整包清单，支持 Registry Basic 或 Bearer challenge。只有显式 `--insecure` 才允许 HTTP 或跳过 TLS 校验。凭证仅从文件读取，不进入命令行参数。

## AMD64 一键部署

一次性迁移场景提供全 AMD64 发行包。解压后无需安装 Helm CLI，只需准备 Harbor 地址、项目及凭据文件和目标 SKS 工作负载集群 kubeconfig：

```bash
./deploy.sh \
  --harbor-address https://harbor.example.com \
  --harbor-project sks-migration \
  --harbor-username-file /secure/sks-migration/harbor-username \
  --harbor-password-file /secure/sks-migration/harbor-password \
  --sks-kubeconfig /secure/sks-migration/target-sks.yaml
```

校验以 `bundle-manifest.json` 为权威清单：所有登记文件必须存在且大小、SHA-256 完全一致，镜像 OCI descriptor 仍逐项校验。为便于一次性部署，解压目录可以额外放置 kubeconfig、凭据文件或下载时保留的原始归档；额外文件既不会被信任，也不会导致已登记物料被误判为篡改。

脚本依次校验离线包、创建或复用 Harbor 项目、导入所有 digest 固定镜像、自动发现 SmartX ELF CSI StorageClass、生成平台凭据、创建 Kubernetes imagePullSecret 和平台 Secret、部署或无损复用默认单实例 MinIO、首次安装时自动登记目标 SKS 与默认对象存储 Profile，再通过内置 Helm Go SDK 安装或升级平台。升级会保留已有对象存储登记，且不依赖可能已经修改过的管理员密码；如升级前删除过默认登记，可在对象存储页重新采用已有 MinIO。完成后输出首次管理员密码、平台 NodePort URL 和默认 MinIO S3 Endpoint。默认 MinIO PVC 为 `100Gi`，可用 `--minio-storage-size` 调整；明确只使用客户现有 S3 时可传 `--skip-default-minio`。实验室自签名 Harbor 可显式添加 `--insecure-registry`；通过 HTTPS 代理暴露界面时添加 `--cookie-secure`。需要固定既有凭据或指定非默认存储类时，可选传入 `--admin-password-file`、`--master-key-file` 和 `--storage-class`。

对象存储页面默认显示一键部署登记的 MinIO。管理员还可以通过“新增 MinIO”创建额外实例，或通过“对接其他 S3”录入兼容 S3 API 的外部对象存储；外部凭据在读写校验通过后加密保存且不会由 API 回显。

## 基础平台安装

`charts/sks-migration-center` 部署 API、Worker、Web 和单实例 PostgreSQL。安装值必须提供：

- 目标 Harbor 中四个镜像的 `repository` 与 `sha256` digest；
- 实际发现到的目标 RWO `global.storageClass`，不可硬编码；
- 已存在 Secret，包含管理员初始密码、32 字节凭证主密钥、数据库 URL 和 PostgreSQL 密码。

平台进程通过内置 Helm Go SDK 安装或升级 Chart，使用内存 kubeconfig、Kubernetes Secret release driver、`atomic + wait`，不调用 Helm CLI。MinIO、NFS CSI、Velero 等 Add-on Chart 会在对应建设窗口加入同一锁定目录。
