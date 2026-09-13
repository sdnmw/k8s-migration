# 窗口 19：Velero 1.18、node-agent、Kopia 与 MinIO BSL

- 状态：工程完成；真实双集群验收待 `sida` kubeconfig
- 日期：2026-09-04
- 版本：Velero 1.18.1、AWS 插件 1.14.0、官方 Helm Chart 12.1.0

## 已完成实现

- 新增独立 Velero 领域服务，通过环境 kubeconfig 与已有 ObjectStorageProfile 在任意已连接 Kubernetes 环境安装 Velero。
- API 通过 Helm Go SDK 安装官方 Chart，启用 node-agent、Kopia、默认文件系统备份和 CSI feature；不调用 Velero CLI。
- Velero 命名空间显式使用 privileged Pod Security，以满足 node-agent 访问 kubelet Pod 卷路径和 MountPropagation 的要求；kubelet root 默认 `/var/lib/kubelet` 且可按集群配置。
- S3 凭证只从加密 Vault解析后写入目标集群 Secret，AddonInstallation 仅持久化 Profile ID、版本和非敏感安装参数。
- 新增动态 Kubernetes CR Adapter：创建和读取 BackupStorageLocation、幂等创建 Backup、Watch Backup 状态、汇总 PodVolumeBackup 字节进度。
- Backup 默认关闭原生卷快照并启用文件系统备份，支持 Plan/Run 身份标签、24 小时 TTL、命名空间范围和同名幂等保护。
- BSL 固定使用 AWS provider、path-style S3、HTTPS endpoint、自定义 CA 与 Secret 引用，并等待 `Available` 后才将安装状态置为 `READY`。
- 新增对象存储/Velero Web 页面：MinIO Profile 列表与读写测试、单实例 MinIO 部署、集群选择、Velero 安装和 Add-on 状态展示。
- 安装前读取现有 `velero` Deployment 与 node-agent 归属；只有本系统 Helm release 才允许幂等升级，检测到未托管安装时在任何 Namespace、Secret 或 Helm 写入前返回明确冲突。

## 离线交付

- Vendored 官方 `velero-12.1.0.tgz`，SHA-256 为 `cd23589ad1b2d25cdd3220f6866b3f6f4c5683c4c09494e76a14700b33f81f83`。
- 固定官方 Velero 1.18.1 multi-platform digest `sha256:11459094b1b21ec7c817b08f8067d9e89380835547915cac9c4132ff05b55b90`。
- 固定官方 AWS 插件 1.14.0 multi-platform digest `sha256:7e82f717f44e89671212e0dfce7e061321c386ea84a33bca64a671670ca6c278`。
- 平台 Helm Chart要求提供 Velero 和 AWS 插件的 Harbor repository + digest，并注入 API 运行环境；离线镜像锁模板包含两者的 OCI layout、SBOM、签名与扫描位置。
- Chart 渲染测试验证 13 个 Velero CRD、Deployment、node-agent DaemonSet、privileged、kubelet hostPath和两个 digest 镜像。

## 自动化验证

- CR Adapter fake dynamic client测试覆盖 BSL、Backup 幂等身份、状态进度、PodVolumeBackup 字节统计和 Watch。
- Velero 服务测试覆盖镜像锁、凭证 Secret、安装值、BSL Available轮询、失败状态持久化和凭证不进入 AddonInstallation。
- API、OpenAPI、主 Chart、官方 Chart与 Web 的单元/构建测试通过。
- 新增 opt-in 双集群验收：从目标 `mw` 读取已部署 MinIO 的 TLS/凭证，分别在目标和源集群安装 Velero，等待两端 BSL Available，再在源集群备份 sentinel ConfigMap并等待 Backup `Completed`。
- 可复现命令：`TEST_SKS_SOURCE_KUBECONFIG=/path/to/sida.yaml TEST_SKS_TARGET_KUBECONFIG=/path/to/mw.yaml make sks-velero-acceptance`。

## 真实验收门禁

已确认 SKS 中 `sida` 工作负载集群为 Ready，可作为源 Kubernetes；本地尚未取得其 kubeconfig，因此没有对该集群做任何 API写入。目标 `mw` 当前已有一套非本系统管理的 Velero 1.13.2、两节点 Ready 的 node-agent，以及两个 Available BSL，已运行约 37 天；本窗口只做了读取检查，没有覆盖或升级它。取得 `sida` kubeconfig后先补窗口 18 的源端 NFS动态供给/重挂载探测，再由用户确定目标旧 Velero 的复用/升级/卸载策略，最后执行双集群 BSL和 Backup CR验收。测试不会打印 kubeconfig、S3 Access Key、Secret Key或 TLS私钥。
