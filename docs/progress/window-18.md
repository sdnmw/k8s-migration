# 窗口 18：外部 NFS Profile、离线 NFS CSI 与读写探测

- 状态：工程完成；真实目标 SKS 验收通过，源集群验收待源 kubeconfig
- 日期：2026-09-04
- 真实目标：`mw` SmartX SKS 工作负载集群

## 已完成实现

- 新增 StorageProfile 完整领域服务、PostgreSQL Repository 和受认证 API：创建、列表、安装和复测。
- 支持 `SMTX_BLOCK`、`EXISTING_NFS_SC`、`EXTERNAL_NFS_SC` 三类配置；外部 NFS 默认 `Retain`，并校验 Server、绝对 Export、Mount Options、StorageClass 冲突和能力快照。
- 外部 NFS 安装流程先检测 `nfs.csi.k8s.io`：存在时复用，不存在时通过 Helm Go SDK 从离线 Chart 安装。
- 创建的 NFS StorageClass 固定 provisioner `nfs.csi.k8s.io`，支持自定义 server、share、mountOptions、reclaimPolicy，开启卷扩容；同名异构配置拒绝覆盖。
- 集群内探测创建临时 64Mi RWX PVC：写 Pod 生成随机内容并落盘，删除写 Pod后创建独立读 Pod重新挂载，核对原内容后删除测试文件；最后清理两个 Pod、PVC 和探测 PV。
- 两个探测 Pod均满足 restricted Pod Security，并显式关闭 SKS k8tz 注入；不使用 ServiceAccount Token。
- 新增存储配置 Web 页面：环境和类型选择、外部 NFS 参数、默认 Retain、安装验证、复测、状态与检查结果。

## 离线交付

- 基于 Kubernetes SIG Storage 官方 `csi-driver-nfs` 4.13.4 Chart 语义，提供 CSIDriver、controller、node DaemonSet、ServiceAccount 和 RBAC。
- 固定 `nfsplugin` 4.13.4、`csi-provisioner` 6.3.0、`csi-resizer` 2.2.0、`livenessprobe` 2.19.0 和 `csi-node-driver-registrar` 2.17.0 的 multi-platform digest。
- 固定官方 BusyBox 1.36.1 multi-platform digest作为读写探测 helper。
- 平台 Chart要求为所有 NFS 组件提供 Harbor repository 与 digest；API 启动时验证所有镜像都包含 `@sha256:`，禁止 tag-only 安装。
- 离线镜像锁模板加入六个组件的 OCI layout、SBOM、签名和扫描路径。

## 真实 SmartX SKS 验收

- 目标集群已存在 NFS CSI Driver 4.13.4，因此按设计走 `REUSED_EXISTING`，未安装第二套冲突驱动。
- 从已有外部 NFS 配置读取 Server/Export，创建独立 `sks-migration-nfs` StorageClass；未修改原有 `nfs-csi-velero-lab`。
- `sks-migration-nfs` 验证结果：provisioner `nfs.csi.k8s.io`、reclaimPolicy `Retain`、allowVolumeExpansion `true`。
- 动态供给 RWX PVC成功；写入 128 字节随机十六进制内容，删除写 Pod，独立读 Pod重新挂载并校验通过。
- 验收结束后无 `migration.smartx.com/probe=nfs` Pod/PVC/PV残留；新 StorageClass 保留供后续 Velero/FSB 使用。
- 可复现命令：`TEST_SKS_KUBECONFIG=/path/to/config make sks-nfs-acceptance`。

## 自动化验证

- 单元测试覆盖 Profile 输入、能力匹配、已有驱动复用、缺失驱动离线安装分支、Retain SC、restricted Pod规范、API 契约及状态持久化。
- NFS CSI Chart渲染测试逐文档解析 YAML，并验证 privileged NFS plugin、Bidirectional mount propagation、CSIDriver、controller、DaemonSet和全部 digest 镜像。
- PostgreSQL 18 集成测试验证 mountOptions 数组、状态更新、环境筛选和外键。
- Playwright 在 1440×900 检查列表页和创建向导；截图为 `output/playwright/storage-profiles-1440x900.png`。
- `GOTOOLCHAIN=local GOPROXY=off make test lint build` 和新增 Go 包 race 测试通过；OpenAPI 有效，既有未来端点 warning 由 10 项降至 6 项。

## 尚待真实条件

源 Kubernetes kubeconfig尚未提供，因此源集群侧的 NFS 网络可达性和 PVC读写需在源环境导入后复用同一探测完成。此项不影响目标 SKS 上的 MinIO、NFS StorageClass 和后续目标 Velero 安装，但窗口 18 的双端真实门禁在源探测前保持未完全关闭。
