# ADR 0003：离线交付与 Add-on 安装边界

- 状态：接受
- 日期：2026-09-04

## 决策

离线发行包以 OCI Image Layout 为镜像载体，以严格 `images.lock.yaml` 固定源 digest、目标 tag、平台、SBOM 和签名路径。包内全部普通文件写入版本化 JSON 清单和 `SHA256SUMS`；归档使用固定时间戳并拒绝符号链接。目标侧在完整校验后通过 Registry v2 API 导入 Harbor。

Kubernetes Add-on 使用固定 Helm 3 Go SDK，不调用目标主机上的 Helm CLI。SDK 从内存 kubeconfig 创建 REST client，以 Kubernetes Secret 存储 release，安装和升级启用 atomic、wait、job wait 和 cleanup-on-fail。Chart 只能从已校验的离线根目录加载，锁定版本必须匹配 Chart version 或 appVersion。

## 理由

- OCI layout 能保存多架构索引，避免把 Docker daemon 当成离线安装依赖。
- digest、逐文件校验、SBOM 和签名路径共同建立从发行到导入的可审计边界。
- 内置 Helm SDK 可统一状态、超时、错误和凭证处理，也避免外部 CLI 版本漂移与任意命令执行面。
- Registry HTTP 和 TLS skip 仅保留为显式隔离实验室选项，生产默认 HTTPS。

## 边界

窗口 16 固化格式、导入器、基础 Chart 和 SDK 管理器。MinIO 固定源码、NFS CSI、Velero、Kompose 与 Kopia 的确切版本、Chart 和镜像条目在各自窗口完成后进入最终 lock；任何缺项都会阻止正式打包。

