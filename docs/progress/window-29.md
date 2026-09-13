# Window 29 — 三栏迁移资源拓扑重构

## 结果

迁移任务详情的资源拓扑已按演示稿重构为“源端应用 → 迁移转换 → 目标 SKS”三栏证据视图。原先把所有资源和映射线堆在同一画布的展示方式已移除；现在资源按业务访问链路自上而下稳定排列，转换规则在中栏单独解释，目标资源与源资源横向对齐。

## 本窗口完成

- 左侧展示源环境、应用或 Compose 项目及源资源。
- 中间展示 Compose 到 Kubernetes 的生成关系，以及 Namespace、Image、StorageClass、IngressClass、NodeLabel、NFS 和 API Version 等映射。
- 右侧展示目标 SKS、目标 Namespace、实际资源状态和验证结果。
- Kubernetes 默认按 `Ingress → Service → Workload → PVC → ConfigMap/Secret` 排列。
- Compose 默认按 `Service → Volume → Config/Secret → Network` 排列。
- 增加“应用视图 / 资源视图”，应用视图聚合主业务链路，资源视图保留全部迁移证据。
- 图例固定在画布右侧，说明资源关系、迁移映射、健康、告警、失败和未迁移状态。
- 点击资源节点或迁移连线时，高亮完整关联链路；无关节点和连线自动灰化。
- 节点详情从右侧抽屉改为画布下方的证据面板，显示属性、健康状态、验证证据、关联资源、映射前后值和 Diff。
- 保留资源搜索、类型过滤、缩放、拖动画布、Fit View 和状态汇总。
- 修正初始画布自动适配导致源端资源被裁切的问题，采用稳定的初始视口。
- 1280px 以下将图例和证据面板改为纵向布局，避免字段覆盖。

## 视觉证据

- 完整三栏拓扑：`output/playwright/topology-three-lane-v2-full.png`
- 资源关联高亮：`output/playwright/topology-three-lane-focused.png`

第二张截图中选中了源端 Service；其目标 Service、Workload 和相关映射保持高亮，PVC 等无关资源降为灰色，底部同步展示该资源的迁移证据。

## 测试门禁

- 前端 14 个交互测试全部通过。
- ESLint 通过。
- TypeScript 与 Vite 生产构建通过。
- Playwright 在 1440×900 视口完成完整拓扑和关联高亮人工校验。
- `mw` 集群滚动升级成功，Helm revision 为 11。
- API、Worker、Web 与 PostgreSQL 全部 Ready。
- 线上 Web 镜像 digest：`sha256:9a27c8450fd946f6a17e98f758d06cd118698d5182e71aa5444a1aa7a16875e8`。

## 最终离线包

`output/sks-migration-center-0.1.0-amd64-topology-style-final/sks-migration-center-0.1.0-linux-amd64.tar.gz`

大小为 609 MiB，SHA-256 为 `a821b10215ceddfa331842f5c799a5bdec39a5f51cdac204bdad4d9971afe8a3`，解包后的校验目录包含 284 个文件。离线导入共处理 16 个 `linux/amd64` 镜像，已有 MinIO 未被重新部署。

平台地址：`http://192.168.118.206:31500`。
