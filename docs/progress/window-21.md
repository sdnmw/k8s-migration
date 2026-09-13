# 窗口 21：CSI Snapshot Data Mover 与 Raw Block门禁

- 状态：工程实现完成，目标 SKS只读能力验收通过
- 日期：2026-09-05
- 当前门禁：自动化门禁和 `mw` 只读探测通过；`sida → mw` 数据迁移仍等待源 kubeconfig

## 已完成

- 能力快照新增结构化 `VolumeSnapshotClass`，同时记录名称、CSI Driver和删除策略；不再仅凭“存在一个快照类名称”判断可迁移。
- 新增 CSI Data Mover运行时探测，分别检查：
  - `snapshot.storage.k8s.io/v1`；
  - Velero `DataUpload`、`DataDownload`、`BackupRepository` API；
  - Velero Server的 `EnableCSI`；
  - node-agent期望和就绪副本数。
- 将能力拆成 `backupReady` 和 `restoreReady`：源端必须具备 CSI快照、DataUpload、BackupRepository、EnableCSI和就绪 node-agent；目标端恢复不错误要求存在同驱动快照类，但必须具备 DataDownload、BackupRepository、EnableCSI和就绪 node-agent。
- 每个源 PVC都按 `StorageClass.provisioner == VolumeSnapshotClass.driver` 精确匹配；仅名称相似或目标端存在快照类均不能放行。
- Raw Block仍禁止 FSB。只有选择 `CSI_DATA_MOVER`、源快照驱动匹配、源上传运行时就绪、目标下载运行时就绪且源节点确认为 Linux时才从 BLOCKER降为 WARNING；Windows或操作系统未知继续阻断。
- 修正 SmartX CSI识别，兼容界面名称 `smtx-elf-csi-driver` 和真实 provisioner `com.smartx.elf-csi-driver`。
- Velero Backup Adapter支持 `snapshotVolumes=true`、`snapshotMoveData=true`、`datamover=velero`，并显式关闭该 Backup的 FSB默认值。
- 新增 DataUpload/DataDownload进度采集，统一写入 `VolumeTransfer(engine=CSI_DATA_MOVER)`；源上传和目标下载都能显示字节数、状态、节点和错误。
- Worker执行前再次实时探测两端 Data Mover运行时，避免仅依赖可能过期的能力快照。
- 环境能力页面新增快照类驱动、Data Mover上传/下载状态、EnableCSI、node-agent副本及节点操作系统展示。

## 验证

- Fake Kubernetes覆盖完整和部分 Velero安装，只有全部必需运行时组件就绪才返回 Ready。
- Velero dynamic fake覆盖 Data Mover Backup CR、DataUpload和DataDownload进度解析。
- Assessment和迁移 Preflight覆盖：
  - 源快照驱动匹配时放行；
  - 驱动不匹配时阻断；
  - 目标无 VolumeSnapshotClass但 DataDownload就绪时允许恢复；
  - Raw Block在 Windows或无法确认 Linux时阻断。
- Executor覆盖实时运行时门禁、Raw Block Linux门禁、Data Mover Backup和不创建 FSB staging Pod。
- OpenAPI、前端类型、ESLint和生产构建通过。

## 真实 `mw` 只读结果

使用已导入的 `mw` kubeconfig执行只读探测，未创建或修改资源：

- SmartX StorageClass：`smtx-elf-csi-driver`
- 匹配快照驱动：`com.smartx.elf-csi-driver`
- Snapshot API：可用
- DataUpload / DataDownload API：可用
- EnableCSI：已启用
- node-agent：`2/2`
- Backup / Restore Data Mover运行时：Ready

这只证明目标集群的存储和现有 Velero运行时具备 Data Mover能力，不代表已完成真实卷迁移。目标仍存在非本系统管理的 Velero 1.13.2；系统不会覆盖它，而且正式执行仍会检查固定的 `migration-minio` BSL。

## 边界与依据

- 目标端不强制要求 CSI快照设施，符合 [Velero CSI Snapshot Data Movement](https://velero.io/docs/v1.18/csi-snapshot-data-movement/) 对恢复端的说明。
- Raw Block仅在非 Windows平台放行；系统采用更保守的 Linux-only能力门禁。
- `sida` kubeconfig尚未下载到工作区，因此源端快照驱动、DataUpload运行时和真实 Raw Block迁移仍未验收。
