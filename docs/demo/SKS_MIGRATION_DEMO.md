# SKS Migration Center 双链路演示

本文档记录两条通过 SKS Migration Center 实际执行的迁移：Kubernetes Namespace 从 `sida` 迁往 `mw`，以及 Docker Compose 应用从主机 `192.168.112.51` 迁往 `mw`。目标集群均使用 SmartX `smtx-elf-csi-driver` 块存储，迁移数据写入目标 SKS 内已部署的 MinIO。

> 演示日期：2026-09-06 至 2026-09-08。截图不包含 kubeconfig、SSH 密码、Harbor 密码或 MinIO 密钥。

## 1. 演示结果

| 链路 | 源应用 | 目标 Namespace | 数据方式 | 最终结果 |
| --- | --- | --- | --- | --- |
| Kubernetes → SKS | PostgreSQL + PostgREST | `demo-postgrest-migrated` | Velero FSB / Kopia | 完成，数据库逐字段一致 |
| Compose → SKS | Spring Boot + Nacos + PostgreSQL | `sks-nacos-demo-migrated` | Compose Kopia，两个 named volume | 完成，数据库和 Nacos 数据一致 |

两条任务都经过：发现、评估、映射、Preflight、在线预同步、停止源业务、最终同步、目标恢复、验证、人工切流确认。平台没有自动修改 DNS、负载均衡或防火墙。

## 2. 平台准备

### 2.1 登录与环境导入

平台使用新的云原生迁移 Logo，登录后首页展示系统与迁移资源概况。

![登录页与新 Logo](../../output/playwright/sks-migration-demo/00-login-new-logo.png)

![平台概览](../../output/playwright/sks-migration-demo/01-overview-menu-fixed.png)

导入源 Kubernetes 集群 `sida`，平台只保存 kubeconfig，不依赖 SKS 私有接口。

![导入 sida](../../output/playwright/sks-migration-demo/03-import-sida-dialog.png)

![sida 连接成功](../../output/playwright/sks-migration-demo/05-sida-connection-check.png)

![sida 能力快照](../../output/playwright/sks-migration-demo/06-sida-capabilities.png)

### 2.2 映射和对象存储

映射配置将两个演示源 Namespace 分别转换为目标 Namespace，并将存储映射到目标 SmartX StorageClass。

![创建存储与 Namespace 映射](../../output/playwright/sks-migration-demo/11-k8s-storage-namespace-mapping.png)

对象存储页默认显示平台安装时已经部署的 MinIO，同时保留“新增 MinIO”和“对接其他 S3”入口。

![已有 MinIO 和两个新增入口](../../output/playwright/sks-migration-demo/21-existing-minio-default.png)

![MinIO 读写验证](../../output/playwright/sks-migration-demo/22-minio-read-write-test.png)

源、目标集群均复用现有 Velero，状态为 READY。

![sida Velero 就绪](../../output/playwright/sks-migration-demo/24-sida-velero-ready.png)

![mw Velero 就绪](../../output/playwright/sks-migration-demo/26-mw-velero-ready.png)

## 3. Kubernetes：PostgreSQL + PostgREST

### 3.1 源应用发现与评估

源应用位于 `sida/demo-postgrest`，包含 PostgREST、PostgreSQL、Service、Secret、ConfigMap 和 RWO PVC。

![选择 Kubernetes 应用](../../output/playwright/sks-migration-demo/07-select-k8s-app.png)

![资源清单](../../output/playwright/sks-migration-demo/08-k8s-inventory-resources.png)

![迁移评估](../../output/playwright/sks-migration-demo/09-k8s-assessment.png)

### 3.2 创建迁移计划

向导依次选择 Kubernetes 来源、PostgREST 应用、目标 `mw`、共享映射和 FSB/Kopia 策略，随后执行 Preflight。

![选择源类型](../../output/playwright/sks-migration-demo/28-k8s-wizard-step1-source-type.png)

![选择源应用](../../output/playwright/sks-migration-demo/29-k8s-wizard-step2-application.png)

![选择目标 mw](../../output/playwright/sks-migration-demo/30-k8s-wizard-step3-target.png)

![评估通过](../../output/playwright/sks-migration-demo/31-k8s-wizard-step3-assessment-passed.png)

![映射与迁移策略](../../output/playwright/sks-migration-demo/32-k8s-wizard-step4-mapping-strategy.png)

![Preflight](../../output/playwright/sks-migration-demo/33-k8s-wizard-step5-preflight.png)

![最终确认](../../output/playwright/sks-migration-demo/35-k8s-wizard-step6-confirmed.png)

### 3.3 迁移执行与人工切流

任务执行在线预同步、最终备份和目标恢复，进度通过 SSE 实时显示。

![在线预同步](../../output/playwright/sks-migration-demo/36-k8s-run-presync.png)

![目标恢复](../../output/playwright/sks-migration-demo/37-k8s-run-restore.png)

目标验证通过后，任务停在“等待人工切流”。此时源工作负载已经停止，但仍可在取消或回滚时自动恢复。

![等待人工切流](../../output/playwright/sks-migration-demo/38-k8s-awaiting-cutover.png)

![确认外部切流](../../output/playwright/sks-migration-demo/39-k8s-cutover-confirmation.png)

![Kubernetes 迁移完成](../../output/playwright/sks-migration-demo/40-k8s-migration-completed.png)

### 3.4 数据完整性

- 源、目标各有 2 条 Todo 记录，业务证明记录为 `K8S-MIGRATION-PROOF-20260906`。
- 规范化 JSON 的源、目标 SHA-256 都是 `b85b4c439cc346fe77cadb82c8af2f31ecae475d48b1b0094ba734ec8374c133`。
- 目标 PostgreSQL PVC 为 Bound，StorageClass 为 `smtx-elf-csi-driver`。
- 最终任务：`f6a8158d-a792-4391-93dc-2fb657ad1890`，状态 `COMPLETED`。

原始验证数据见 [源记录](evidence/k8s-source-records.json)、[目标记录](evidence/k8s-target-records.json) 和 [离线 HTML 迁移报告](../../output/reports/migration-report-f6a8158d-a792-4391-93dc-2fb657ad1890.html)。

## 4. Compose：Spring Boot + Nacos + PostgreSQL

### 4.1 应用结构和数据卷

源主机运行一个完整应用系统：Spring Boot 提供 Web/REST API，Nacos 提供服务注册，PostgreSQL 持久化业务记录。Compose 使用两个 named volume：`postgres-data` 和 `nacos-data`。

![注册 Compose 主机](../../output/playwright/sks-migration-demo/13-register-compose-host-form.png)

![主机连接成功](../../output/playwright/sks-migration-demo/15-compose-host-connection-check.png)

![注册 Compose 应用](../../output/playwright/sks-migration-demo/16-register-compose-app-dialog.png)

![Compose 资源清单](../../output/playwright/sks-migration-demo/18-compose-inventory.png)

### 4.2 创建迁移计划

向导选定 Compose 主机与应用，目标为 `mw`，Namespace 映射为 `sks-nacos-demo-migrated`，卷策略为 Compose Kopia，并启用在线预同步。

![选择 Compose 类型](../../output/playwright/sks-migration-demo/41-compose-wizard-step1-source-type.png)

![选择 Compose 主机](../../output/playwright/sks-migration-demo/42-compose-wizard-step1-host.png)

![选择应用](../../output/playwright/sks-migration-demo/43-compose-wizard-step2-application.png)

![目标与评估](../../output/playwright/sks-migration-demo/45-compose-wizard-step3-assessment-passed.png)

![映射与 Kopia 策略](../../output/playwright/sks-migration-demo/46-compose-wizard-step4-mapping-strategy.png)

![Preflight](../../output/playwright/sks-migration-demo/47-compose-wizard-step5-preflight.png)

![确认迁移计划](../../output/playwright/sks-migration-demo/49-compose-wizard-step6-confirmed.png)

### 4.3 执行、业务验证与切流

平台先在线快照两个 volume，停止 Compose 服务后执行最终增量快照，再把 Kompose 候选资源经过“资源转换规则”处理后部署到目标 Namespace。

![最终成功任务启动](../../output/playwright/sks-migration-demo/58-compose-corrected-run-started.png)

![等待人工切流](../../output/playwright/sks-migration-demo/59-compose-final-awaiting-cutover.png)

迁移后的 Spring Boot 页面可读取 PostgreSQL 的两条原始记录，后台健康接口同时确认 Nacos 注册成功。

![目标 Spring Boot 应用](../../output/playwright/sks-migration-demo/60-compose-target-application.png)

![人工切流确认](../../output/playwright/sks-migration-demo/61-compose-cutover-confirmation.png)

![Compose 迁移完成](../../output/playwright/sks-migration-demo/62-compose-migration-completed.png)

### 4.4 数据完整性

- Spring Boot 健康结果：`databaseRecords=2`、`nacosRegistered=true`。
- PostgreSQL 证明记录为 `COMPOSE-MIGRATION-PROOF-20260906`。
- PostgreSQL 源、目标规范化 JSON SHA-256 均为 `25779d872167be899b633142b1fb21b2012bc206f9e8f9a7573fc3d42658d9de`。
- Nacos volume 源、目标标记 SHA-256 均为 `e480cf62ff782b2429e41a83c1e7b26a425959ba92306dc1da59174876a6ee94`。
- 目标 `app`、`nacos`、`postgres` Deployment 全部为 1/1 Ready；两个 PVC 均使用 `smtx-elf-csi-driver`。
- 最终任务：`df735583-d04a-41e8-a7ea-30cb25bef690`，状态 `COMPLETED`，传输 `54.8 MiB`。
- 人工确认切流后，源 Compose 容器保持停止。

验证数据见 [源数据库记录](evidence/compose-source-records.json)、[目标数据库记录](evidence/compose-target-records.json)、[目标健康结果](evidence/compose-target-health.json)、[校验值](evidence/checksums.txt) 和 [离线 HTML 迁移报告](../../output/reports/migration-report-df735583-d04a-41e8-a7ea-30cb25bef690.html)。

## 5. 故障与自动恢复演示

正式成功前主动保留了一次故障链路，用于证明平台不会在业务验证失败时把用户留在源业务已停止的状态。

1. 第一次运行识别到源端 Kopia 不信任 MinIO CA，平台明确标记失败。
2. 修复 CA 后，目标资源与数据卷恢复成功，但业务级检查发现服务端口定义缺失。
3. 在人工切流前执行回滚，平台自动重新启动源 Compose，目标资源保留用于诊断。
4. 修正 Compose `expose` 后重新发现并执行最终任务。

![CA 故障被识别](../../output/playwright/sks-migration-demo/51-compose-ca-failure-diagnosed.png)

![预同步与最终同步](../../output/playwright/sks-migration-demo/52-compose-presync-final-sync.png)

![目标待业务检查](../../output/playwright/sks-migration-demo/53-compose-platform-awaiting-app-validation.png)

![切流前回滚确认](../../output/playwright/sks-migration-demo/54-compose-application-check-rollback.png)

![源业务自动恢复](../../output/playwright/sks-migration-demo/55-compose-source-restored.png)

## 6. 迁移证据中心升级验收

任务详情默认展示 Argo CD Application 风格的源端/目标端双拓扑。连线表达依赖和生成关系，节点颜色表达逐资源结果，“已映射”标记可展开查看 Namespace、镜像、StorageClass 等源值、目标值和实际生效结果。

### 6.1 历史任务拓扑补建

历史 Compose 任务从应用 Inventory、映射配置、关键事件和目标当前状态补建证据。页面同时明确提示“历史证据补建”，不会把当前观察冒充为执行时刻快照。

![Compose 三服务、网络、双卷和目标 K8s 资源拓扑](../../output/playwright/sks-migration-evidence/01-compose-resource-topology.png)

历史 K8s 任务完整展示 PostgreSQL/PostgREST 的 Deployment、Service、PVC 和配置依赖；同名 SmartX StorageClass 映射也作为显式证据显示，并验证目标实际值为 `smtx-elf-csi-driver`。

![K8s PostgREST 双端资源拓扑](../../output/playwright/sks-migration-evidence/03-k8s-resource-topology.png)

![SmartX StorageClass 映射已生效](../../output/playwright/sks-migration-evidence/04-k8s-storageclass-mapping.png)

### 6.2 原生快照和重试诊断

另行执行可丢弃任务 `b3435409-0a92-4a4a-bed7-96f46f659cdd`，用于验证新版本不是只依赖历史补建：

- Run 创建时即保存 `snapshotOrigin=NATIVE`，源端 6 个节点，目标期望拓扑 11 个节点。
- 最终目标资源 11/11 成功，漂移、失败和缺失均为 0。
- 每个 Worker 步骤均保存 attempt、心跳、开始结束时间和关键事件。
- Validation 第一次因 `app` Deployment 尚未 Ready 自动进入重试，第二次成功；界面和报告均保留直接原因。
- 完成人工切流后证据固化，刷新当前状态只形成独立观察层。

![原生任务完成后的双端拓扑](../../output/playwright/sks-migration-evidence/10-native-completed-topology.png)

![执行时序与关键事件](../../output/playwright/sks-migration-evidence/08-native-execution-timeline-full.png)

![Validation 两次尝试和直接失败原因](../../output/playwright/sks-migration-evidence/09-native-validation-retry-detail.png)

![两个 Compose Volume 的 Kopia 传输和校验结果](../../output/playwright/sks-migration-evidence/11-native-data-migration.png)

![原生快照来源、任务时间和当前状态检查](../../output/playwright/sks-migration-evidence/12-native-task-info.png)

终态再次从目标应用内部调用 `/api/health` 和 `/api/records`：返回 `databaseRecords=2`、`nacosRegistered=true`，且两条业务记录的 ID、内容和创建时间与源端完全一致；`nacos-data`、`postgres-data` 两个 PVC 均为 `Bound`，StorageClass 均为 `smtx-elf-csi-driver`。

原生任务的 [单文件 HTML 诊断报告](../../output/reports/migration-report-b3435409-0a92-4a4a-bed7-96f46f659cdd.html) 内联全部 CSS、SVG 与交互脚本，不引用 CDN、字体、图片或平台 API，断网后仍可浏览拓扑、映射、步骤、卷明细和日志时间线。

### 6.3 列表与拓扑交互优化

迁移任务列表采用固定列宽和单元格省略策略，长任务名、环境名和 Namespace 不再覆盖相邻字段；完整值可通过悬浮提示查看。

![迁移任务列表字段不再相互覆盖](../../output/playwright/sks-migration-evidence/23-migration-table-final.png)

资源拓扑的两侧都改为自上而下的调用层级。同侧依赖使用上下端口，跨端迁移使用右到左的独立映射端口：Kubernetes 按 Service → Deployment → PVC 等依赖向下展开；存在 Ingress、ConfigMap、Secret 等引用时，会分别位于入口层和工作负载依赖层。Compose 按入口 Service → depends_on 服务 → Volume/Network 向下展开，目标侧按 Service → Deployment → PVC 展开。

![Kubernetes 双端纵向拓扑](../../output/playwright/sks-migration-evidence/19-k8s-topology-final.png)

![Compose 与目标 Kubernetes 双端纵向拓扑](../../output/playwright/sks-migration-evidence/20-compose-topology-final.png)

点击节点或迁移连线后，画布只保留两跳内的上下游依赖、跨端映射和目标生成资源为高亮状态，其余节点与连线灰化；点击画布空白可恢复全局视图。下图聚焦 `postgres` 的 Compose Service → 目标 Deployment 映射，Nacos 和无关的目标资源已经灰化。

![点击迁移连线后聚焦 PostgreSQL 关联链路](../../output/playwright/sks-migration-evidence/21-compose-mapping-focus-final.png)

点击资源节点时除聚焦关联链路外，还会打开证据抽屉，展示资源标识、迁移状态、字段映射、目标实际值和资源属性。

![点击资源节点后聚焦链路并打开证据抽屉](../../output/playwright/sks-migration-evidence/22-compose-node-focus-final.png)

### 6.4 资源映射与管理员密码

迁移策略中的“存储映射”已统一更名为“资源映射”，因为该配置同时维护 StorageClass、外部 NFS、Namespace、IngressClass 和 NodeLabel，并不只处理存储。列表标题、创建/编辑弹窗和规则列名称同步更新。

![资源映射页面和导航命名](../../output/playwright/sks-migration-evidence/24-resource-mapping-renamed.png)

右上角管理员菜单新增“修改密码”。弹窗要求当前密码、新密码和二次确认；新密码至少 12 个字符，成功后使当前及其他管理员会话全部失效并返回登录页。演示验收只打开并关闭弹窗，没有提交或改变现有密码。

![管理员修改密码入口](../../output/playwright/sks-migration-evidence/25-admin-change-password-modal.png)

## 7. 演示结论

- 两条真实迁移链路均完成，并由业务数据而非仅 Pod Ready 状态证明数据完整。
- Kubernetes PVC 和 Compose named volume 均落到 SmartX 块存储。
- 平台正确执行预同步、停机最终同步和人工切流门禁。
- 切流前失败可恢复源业务；切流确认后源业务保持停止。
- 迁移结论由逐资源拓扑、映射生效结果、步骤重试和数据校验共同给出，不再仅依赖 Run 总状态。
- 三份 HTML 报告均为完全自包含文件；平台报告、截图与证据文件不包含凭证。
