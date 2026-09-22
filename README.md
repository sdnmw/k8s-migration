# SKS Migration Center

SKS Migration Center 是面向 SmartX SKS 工作负载集群的一次性应用迁移平台，支持 Kubernetes Namespace/资源组合和 Docker Compose 应用迁移。平台只连接用户导入的源、目标环境，不操作 CAPI 管控集群，也不自动修改 DNS、负载均衡或外部防火墙。

## 第一部分：离线部署指南

### 1. 准备条件

部署前需要准备：

- 一台可以执行 Linux AMD64 二进制的部署终端；
- 目标 SKS 工作负载集群的 kubeconfig；
- 目标 Harbor 地址、项目名称和具有项目创建、镜像推送权限的账号；
- 部署终端能够访问 Harbor 和目标集群 API；
- 目标集群节点能够从 Harbor 拉取镜像；
- 目标集群中可用的 SmartX ELF CSI RWO StorageClass，或明确指定其他 RWO StorageClass。

完整离线包发布在 [GitHub Release v0.2.0](https://github.com/sdnmw/k8s-migration/releases/tag/v0.2.0)：

- `sks-migration-center-0.2.0-linux-amd64.tar.gz`
- `sks-migration-center-0.2.0-linux-amd64.tar.gz.sha256`

归档包含 16 个 `linux/amd64` OCI 镜像、平台和 Add-on Helm Chart、镜像锁、静态安装器及校验清单。安装过程不访问公网，也不要求目标终端安装 Docker、Helm、Skopeo 或 Crane。

### 2. 校验并解压

```bash
sha256sum -c sks-migration-center-0.2.0-linux-amd64.tar.gz.sha256

mkdir -p sks-migration-center-0.2.0
tar -xzf sks-migration-center-0.2.0-linux-amd64.tar.gz \
  -C sks-migration-center-0.2.0
cd sks-migration-center-0.2.0
```

当前发行归档 SHA-256：

```text
457130ecef2c48465d9313406c0999d4dbf8c575670c57c43b418fbefbdf8778
```

### 3. 准备凭据文件

Harbor 用户名和密码通过文件传入，避免出现在进程参数和 Shell 历史中：

```bash
install -d -m 0700 /secure/sks-migration
printf '%s' '<Harbor 用户名>' > /secure/sks-migration/harbor-username
printf '%s' '<Harbor 密码>' > /secure/sks-migration/harbor-password
install -m 0600 /path/to/target-sks.yaml /secure/sks-migration/target-sks.yaml
chmod 0600 /secure/sks-migration/harbor-username \
  /secure/sks-migration/harbor-password
```

### 4. 一键部署

```bash
./deploy.sh \
  --harbor-address 192.168.112.28 \
  --harbor-project sks-migration \
  --harbor-username-file /secure/sks-migration/harbor-username \
  --harbor-password-file /secure/sks-migration/harbor-password \
  --sks-kubeconfig /secure/sks-migration/target-sks.yaml \
  --insecure-registry
```

上例的 `--insecure-registry` 仅用于实验室自签名或不受信任证书的 Harbor；受信任 HTTPS Harbor 不需要该参数。安装器严格校验离线清单中登记的文件和镜像，但允许解压目录中额外放置 kubeconfig、凭据文件或下载时保留的原始压缩包；这些额外文件不会参与信任判断。

安装器会依次完成：

1. 校验归档文件清单、镜像锁和 OCI descriptor；
2. 创建或复用 Harbor 项目，并导入全部 digest 固定的 AMD64 镜像；
3. 发现目标集群的 SmartX ELF CSI StorageClass；
4. 创建平台 Namespace、imagePullSecret 和应用 Secret；
5. 部署 PostgreSQL、API、Worker 和 Web；
6. 默认部署单实例 MinIO，并把它登记为平台已有对象存储；
7. 输出管理界面地址、首次管理员密码和默认 MinIO S3 Endpoint。

常用可选参数：

| 参数 | 用途 |
| --- | --- |
| `--storage-class <name>` | 不使用自动发现结果，明确指定平台和 MinIO 的 RWO StorageClass |
| `--minio-storage-size <size>` | 设置默认 MinIO PVC 容量，默认 `100Gi` |
| `--skip-default-minio` | 不部署默认 MinIO，安装后在界面中对接其他 S3 |
| `--namespace <name>` | 修改平台 Namespace，默认 `sks-migration-center` |
| `--admin-password-file <file>` | 覆盖管理员密码；首次安装省略时默认使用 `SmartX@123456`，升级且密码已修改时应提供当前密码以补登记默认对象存储 |
| `--master-key-file <file>` | 使用指定的 32 字节 Base64 主密钥；省略时自动生成 |
| `--cookie-secure` | 平台通过 HTTPS 暴露时为登录 Cookie 启用 Secure 属性 |
| `--insecure-registry` | 仅在可信实验环境中允许 HTTP Harbor 或跳过 Harbor TLS 校验 |

如果部署时跳过默认 MinIO，进入“系统设置 → 对象存储”，选择“对接其他 S3”；需要平台再管理一个独立实例时选择“新增 MinIO”。重复执行同一 `deploy.sh` 会复用现有 Secret、对象存储登记和持久化数据，并通过内置 Helm SDK 执行升级。

### 5. 安装后检查

```bash
kubectl --kubeconfig /secure/sks-migration/target-sks.yaml \
  -n sks-migration-center get pod,pvc,svc

kubectl --kubeconfig /secure/sks-migration/target-sks.yaml \
  -n sks-migration-center rollout status deployment/sks-migration-center-api

kubectl --kubeconfig /secure/sks-migration/target-sks.yaml \
  -n sks-migration-center rollout status deployment/sks-migration-center-worker

kubectl --kubeconfig /secure/sks-migration/target-sks.yaml \
  -n sks-migration-center rollout status deployment/sks-migration-center-web
```

更完整的离线包组装、Harbor 导入、对象存储和升级说明见 [`deploy/offline/README.md`](./deploy/offline/README.md)，迁移故障处理见 [`docs/operations/migration-failure-lessons.md`](./docs/operations/migration-failure-lessons.md)，NFS 卷迁移实测步骤与截图见 [`NFS_MIGRATION_GUIDE.md`](./NFS_MIGRATION_GUIDE.md)。

## 第二部分：迁移逻辑

### 1. 平台迁移时序

创建迁移时，用户依次选择源环境、源应用或资源组合、目标 SKS、可选资源映射、迁移策略和验证策略。未选择映射时，Compose 按项目名自动创建 Namespace；Kubernetes 保留源 Namespace 和资源字段，并在需要时使用目标集群默认值。只有命中的映射规则才会改写资源。Assessment 中存在未解决的 BLOCKER 时不能创建迁移计划。

每次 Migration Run 固定执行以下步骤，界面的“执行时序”与这些状态一一对应：

```text
PREFLIGHT
  → PRESYNC（启用预同步时）
  → QUIESCE
  → FINAL_BACKUP
  → TRANSFER（存在卷数据时）
  → TRANSFORM
  → RESTORE
  → VALIDATION
  → AWAIT_CUTOVER
  → COMPLETED
```

| 平台步骤 | 实际动作 |
| ---|---|
| `PREFLIGHT` 执行前检查 | 重新检查源、目标、对象存储、容量、镜像、映射和迁移组件；发现 BLOCKER 立即停止 |
| `PRESYNC` 在线预同步 | 在源业务仍运行时先复制大部分卷数据，缩短最终停机窗口 |
| `QUIESCE` 停止源业务 | 记录源工作负载状态后停止有状态源业务；无卷迁移不会停止源端 |
| `FINAL_BACKUP` 最终备份 | 源业务停止后执行增量备份，形成迁移使用的最终一致数据集 |
| `TRANSFER` 数据传输 | 确认 Velero FSB、CSI Data Mover 或 Kopia 数据已写入目标可访问的对象存储 |
| `TRANSFORM` 资源转换 | 固化目标期望资源，应用 Namespace、Image、StorageClass、IngressClass、NodeLabel 和 NFS 映射 |
| `RESTORE` 目标恢复 | 在目标 Namespace 创建资源、恢复 PVC 数据，并启动目标工作负载 |
| `VALIDATION` 业务验证 | 汇总全部 Deployment、StatefulSet、PVC、Service、Ingress、映射及 HTTP/TCP 检查结果 |
| `AWAIT_CUTOVER` 等待人工切流 | 目标已经可用，但平台等待管理员完成外部 DNS/LB/防火墙切换并确认 |
| `COMPLETED` 完成 | 固化源、转换、目标拓扑和迁移证据，可下载自包含 HTML 报告 |

任务百分比只表示步骤执行进度，最终成功与否以逐资源验证和任务终态为准。任务详情中的资源拓扑、执行时序、数据迁移和任务信息会持续记录资源状态、映射变化、重试、心跳和错误原因。

### 2. Kubernetes → SKS

1. **发现应用**：平台可以按 Namespace 自动发现业务拓扑，也可以由用户在一个 Namespace 中手选 Deployment、StatefulSet、Service、Ingress、PVC、ConfigMap、Secret 等资源组合。创建 Run 时会固化 Inventory 和 MappingProfile，后续重新发现不会改写本次证据。
2. **能力与兼容性评估**：检查 Kubernetes/API 版本、节点架构、StorageClass、CSI Driver、VolumeSnapshotClass、IngressClass、镜像和安全约束。文件系统卷默认使用 Velero FSB/Kopia；满足条件时可选择 CSI Snapshot Data Mover；不能安全处理的 raw block 卷会成为 BLOCKER。
3. **预同步**：Velero 在源集群创建预同步 Backup。未挂载 PVC 会使用临时 staging Pod；node-agent 将卷数据写入目标集群可访问的 S3/MinIO。
4. **停止源业务**：存在 PVC 时，平台保存所选 Deployment/StatefulSet 的副本数并缩容到 `0`。手选资源只停止迁移清单中的工作负载。没有 PVC 时不停止源业务，此步骤记录为无需停机。
5. **最终备份和传输确认**：源端静止后创建最终 Backup，并等待 FSB/DataUpload 完成。源、目标 Velero 通过同一对象存储同步备份元数据。
6. **转换与恢复**：恢复前只应用实际命中的 Namespace 和 StorageClass 映射；未匹配时保留源值或使用目标默认 StorageClass。Velero Restore 完成后再对目标资源实际应用 Registry/Image、IngressClass、NodeLabel 和 NFS 等可变字段映射，并逐项确认映射已经生效。业务 Namespace 不会被平台额外强制设置 Pod Security Admission 等级。
7. **验证**：平台等待工作负载 Ready、PVC Bound，检查 Service selector、Ingress 后端、ConfigMap/Secret/RBAC 存在性以及配置的 HTTP/TCP 探测。验证会收集所有资源结果，而不是遇到第一个错误就停止。
8. **等待切流**：验证通过后进入 `AWAITING_CUTOVER`，由管理员在平台外修改 DNS、负载均衡或防火墙，再点击确认切流。

### 3. Docker Compose → SKS

1. **发现应用**：Compose 主机通过 SSH 导入。平台使用受控命令读取 `docker compose ls -a`，发现运行中和已停止的项目，并在源主机解析 include、override、`.env` 和 `env_file`。单个项目配置失效时只跳过该项目，不再阻断同一主机上的其他应用；手动上传仅用于不引用外部文件的自包含 compose 文件。平台解析 Service、镜像、端口、`depends_on`、网络、named volume、允许目录内的 bind mount、config、secret 和 profiles。
2. **镜像收口与预检**：不论服务使用本地 build 镜像还是公有镜像，平台都先从源主机解析实际镜像，推送到安装时关联的 Harbor。平台优先创建 `<安装项目>-compose-<应用名>` 公开项目；无项目创建权限时降级到 `Harbor/library`。只有真实 image push 或后续目标拉取失败才阻断。之后平台在目标集群的隔离 Job 中运行 Kompose，再经过 Assessment、Transform 和 Diff 生成候选 Kubernetes 清单。源 compose 文件不会被改写，前端也不会向 SSH 主机开放任意 Shell。
3. **预同步**：存在 named volume 或允许的 bind mount 时，受控 Kopia 辅助容器在 Compose 仍运行期间把卷数据预同步到 S3/MinIO。无数据卷应用跳过实际数据传输。
4. **停止源业务**：存在需迁移的数据卷时执行受控的 Compose stop，并记录停止事件。无卷应用保持运行。
5. **最终同步**：源服务停止后对每个卷执行增量 Kopia Snapshot，确保迁移数据对应停机时刻的文件状态。平台保证正常停止后的文件级一致性，不提供数据库在线复制。
6. **转换与恢复**：Compose Service 转换为 Deployment/Service，volume 或 bind mount 转换为 PVC，config/secret 转换为 ConfigMap/Secret；随后应用 Registry、StorageClass、Namespace 和 Ingress 等映射。平台先恢复 PVC 数据，再恢复工作负载副本。
7. **验证与切流**：逐项检查生成的 Kubernetes 资源、PVC、服务端点及 HTTP/TCP 探测，通过后等待管理员人工切流确认。

### 4. 失败、取消、重试和恢复源端

- 在停止源业务之后、人工切流之前发生失败或取消时，平台进入 `ROLLING_BACK`：Kubernetes 恢复之前记录的副本数；Compose 执行受控的启动动作。目标资源默认保留，便于诊断。
- “恢复源端”使用与自动回滚相同的幂等逻辑，也可以在迁移完成后使用；它只恢复源业务，不自动删除目标资源，也不会反向同步目标端后来产生的数据。
- 没有 PVC/Compose 数据卷的任务不会停止源业务，因此恢复源端时会明确显示“无需恢复”。
- 失败、取消或已完成任务可以“重新执行”，生成新的 Run 并保留旧 Run 的拓扑、事件和报告；需要调整选择、映射或策略时先“编辑任务”再重新执行。
- 人工切流仅表示管理员已经在平台外完成访问入口切换。点击确认后任务才进入 `COMPLETED`，平台本身不会修改 DNS、负载均衡或防火墙。

## 第三部分：项目目录说明

| 路径 | 作用 |
|---|---|
| [`api/`](./api) | OpenAPI 契约；前后端请求、响应、错误码和核心 Schema 的唯一接口来源 |
| [`cmd/server/`](./cmd/server) | Go API Server 入口，负责 HTTP API、登录会话、SSE、报告和依赖装配 |
| [`cmd/worker/`](./cmd/worker) | 独立 Worker 入口，负责 PostgreSQL 任务租约、步骤执行、重试与心跳 |
| [`cmd/offline/`](./cmd/offline) | 离线包 pack/verify/import/deploy/sign 命令和内置 Helm 部署逻辑 |
| [`internal/domain/`](./internal/domain) | Environment、Application、Assessment、Mapping、Migration、Storage 等领域模型和状态机 |
| [`internal/api/`](./internal/api) | REST/SSE Handler、错误响应和 HTML 迁移报告生成 |
| [`internal/migration/`](./internal/migration) | MigrationPlan/Run 编排、Kubernetes/Compose 执行器、拓扑证据和故障诊断 |
| [`internal/adapter/`](./internal/adapter) | Kubernetes、Velero、SSH 和 S3 外部系统适配器 |
| [`internal/assessment/`](./internal/assessment) | 兼容性评估规则、评分、WARNING 和 BLOCKER 判定 |
| [`internal/transform/`](./internal/transform) | Kubernetes 清单规范化、资源映射、字段改写和 YAML Diff |
| [`internal/repository/`](./internal/repository) | Repository 接口及 PostgreSQL 实现，包括任务、事件、审计和拓扑快照 |
| [`internal/database/`](./internal/database) | PostgreSQL 连接、迁移执行器和数据库 migration SQL |
| [`internal/addon/`](./internal/addon) | MinIO、NFS CSI、Velero 等 Add-on 的 Helm SDK 管理 |
| [`internal/security/`](./internal/security) | 密码哈希、envelope encryption 和敏感信息脱敏 |
| [`internal/offline/`](./internal/offline) | 离线归档、OCI 镜像锁、Registry 导入、签名和完整性校验 |
| [`internal/acceptance/`](./internal/acceptance) | 双集群、真实 SKS、MinIO、NFS、Velero 和 Compose 验收测试入口 |
| [`web/`](./web) | React、TypeScript、Ant Design 管理界面和 Vitest 测试 |
| [`deploy/charts/`](./deploy/charts) | 平台、MinIO、NFS CSI 和 Velero 的离线 Helm Chart |
| [`deploy/offline/`](./deploy/offline) | 离线组件清单、镜像锁模板、一键部署入口和详细部署文档 |
| [`build/`](./build) | 平台/工具镜像 Dockerfile 及 MinIO、Kompose、Kopia 源镜像锁 |
| [`scripts/`](./scripts) | 开发、发行包构建、Kind E2E、Chart 固定和辅助验证脚本 |
| [`demo/`](./demo) | Kubernetes PostgREST 与 Compose Spring Boot/Nacos 示例应用 |
| [`docs/adr/`](./docs/adr) | 架构决策记录 |
| [`docs/operations/`](./docs/operations) | 故障注入、迁移经验、升级和卸载手册 |
| [`docs/progress/`](./docs/progress) | 各建设窗口的实现与验收历史记录 |
| [`docs/demo/`](./docs/demo) | Demo 迁移演示和数据校验证据 |
| [`docs/releases/`](./docs/releases) | 发行说明、归档摘要和校验值 |
| [`sks-migration-center-spec/`](./sks-migration-center-spec) | 产品规格、领域模型、UI 规格和原始任务拆解 |
| [`.github/workflows/`](./.github/workflows) | GitHub CI 配置 |
| [`Makefile`](./Makefile) | 本地 bootstrap、测试、构建、启动和清理的统一入口 |
| [`docker-compose.yml`](./docker-compose.yml) | 本地开发环境的 PostgreSQL、API、Worker 和 Web 编排 |

本地开发可执行：

```bash
cp .env.example .env
make bootstrap
make test
make build
make dev
```

默认开发入口为 Web `http://localhost:3000`、API `http://localhost:8080`。`make dev` 生成的本地管理员密码、主密钥和运行数据位于被 Git 忽略的 `.data/` 中。
