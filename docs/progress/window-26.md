# Window 26 — 最终 E2E、SKS 实测与离线发行收口

## 结果

Window 26 的工程门禁、Kubernetes 主链路、Docker Compose 主链路、平台本体真实部署和一次性 AMD64 离线发行验收均已完成。按操作者明确接受的一次性工具边界，正式签名与漏洞扫描不再作为本次交付阻断项。

真实 `sida → mw` 已完成资源控制链、Velero 元数据迁移、SmartX RWO 块 PVC FSB 和 RWX NFS PVC FSB。真实 `.51 Compose → mw` 已完成 Registry 映射、Kompose 转换、Kopia 预同步、停机最终同步、SmartX 块 PVC 恢复、文件校验与源端回滚。所有验收均使用唯一 Namespace/run-id/Compose project，结束后清理测试资源。

## 本窗口完成

- 新增双 Kind 控制链 E2E 和 GitHub Actions 任务。Kind v0.32.0 二进制校验官方 SHA-256，节点镜像固定 digest，业务镜像预先加载，不依赖测试中的公网拉取。
- 同一控制链测试可直接连接两套真实 kubeconfig，覆盖资源 Server-Side Apply、Pod 就绪、源端副本快照、停机、恢复以及目标 ConfigMap/Secret 一致性。
- 离线归档增加 Ed25519 签名与可信公钥校验；签名覆盖归档 SHA-256，拒绝归档篡改、错误公钥、未知字段和尾随内容。
- 新增“复用已有 Velero”API 与 Web 操作，不覆盖外部 Deployment/DaemonSet，只创建迁移专用凭证 Secret 和 BSL。
- BSL 访问模式按角色固定：源端 `ReadWrite`，目标端 `ReadOnly`。
- 兼容 Velero 1.13 FSB：目标迁移 Namespace 在恢复阶段采用 `baseline` PSA，允许现有 `restore-wait` 辅助容器工作。
- StorageClass 映射改为唯一共享 ConfigMap，各 run 通过注解持有映射；支持并发合并、冲突拒绝和按 run 清理。
- Velero 备份清理改为源端创建 `DeleteBackupRequest`，目标端只清理同步 CR，确保 MinIO 对象真正删除。
- 对象存储/Velero 页面按 1440×900 实机校准，补充外部管理状态与复用入口。
- 迁移任务列表与概览移除前端示例数据，新增后端聚合读模型，一次返回计划、运行、源/目标环境和应用展示字段；任务状态、进度、数据量及环境健康状态均来自真实 API。
- Compose SSH 环境支持密码或私钥二选一认证；密码与私钥使用相同的加密凭证存储且不在 API 响应中回显。
- SSH 主机 key 协商固定优先 Ed25519，避免同一主机同时发布 RSA/ECDSA/Ed25519 时校验到错误 key；受控命令失败会返回限长诊断信息。
- Kopia 0.23.1 快照 JSON 解析兼容 pretty-print 及前后状态文本；SSH 合并输出缓冲增加并发保护。
- 离线 Registry v2 importer 修复 blob upload URL 尾斜杠丢失导致的 Harbor `404`，并新增回归测试。
- 使用锁定官方二进制构建 amd64 Kompose 1.38.0 与 Kopia 0.23.1 OCI Layout；测试离线包通过清单校验并分别导入 `.28/sida` 与 `.30/sks`，Harbor 返回 digest 与锁文件完全一致。
- 使用 amd64 digest 固定镜像将 API、Worker、Web 和 PostgreSQL 18.3 导入 `.30/sks`，并通过 Helm Go SDK 将完整平台部署到真实 `mw` 工作负载集群。
- 平台 PostgreSQL 使用 `smtx-elf-csi-driver` 动态创建 20 GiB RWO PVC；API、Worker、Web、PostgreSQL 四个 Pod 全部 Ready。
- 真实 NodePort `http://192.168.118.206:31500` 已完成 Web 反向代理、管理员登录 Cookie 和 `/api/v1/auth/me` 会话验证。该一次性实验室部署显式使用 HTTP Cookie；生产 TLS 部署仍应保持 `COOKIE_SECURE=true`。
- SKS kubelet 对 distroless 字符串用户进行严格校验，Chart 已为 API/Worker 固定 UID/GID `65532`，并为官方 PostgreSQL 固定 UID/GID `999`；同一真实集群复测通过。
- 完整离线包包含平台、PostgreSQL、MinIO、Velero、AWS 插件、NFS CSI 全套 sidecar、BusyBox、Kompose、Kopia 和迁移 helper 共 16 个 OCI Layout；全部为 `linux/amd64`。
- 离线包内置静态链接 Linux x86-64 安装器和 `deploy.sh`。必填输入仅为 Harbor 地址、项目、凭据文件和目标 SKS kubeconfig；安装器自动创建/复用 Harbor 项目、导入镜像、发现 SmartX ELF CSI StorageClass、生成平台凭据并通过 Helm Go SDK 安装或升级。
- 使用最终解压包在 AMD64 Linux 容器中实跑一键部署，16/16 镜像从本地 OCI Layout 导入 `.30/sks`，平台 release 升级到 revision 4，MinIO 页面运行时默认值已正确改写为 Harbor AMD64 digest。
- 另在全新 `sks-migration-center-offline-e2e` 命名空间仅传入 Harbor 地址/项目/凭据文件、目标 kubeconfig 和实验室 TLS 开关执行首次安装；安装器自动发现 `smtx-elf-csi-driver`、自动生成凭据、创建并绑定 20 GiB RWO PVC，API/Worker/Web/PostgreSQL 4/4 Running，管理员登录及 `/api/v1/auth/me` 通过。验收后已删除该临时命名空间及其一次性 PVC，正式实例仍为 4/4 Running。
- 一键安装器进一步闭环默认对象存储：全新安装时部署默认 MinIO，升级时探测并无损复用既有 `sks-migration-minio`，自动注册目标 SKS、重建缺失的对象存储 Profile 并执行 S3 读写校验。真实 `mw` 升级到 revision 6 后默认 MinIO 已显示为 `READY`，原 20 GiB `smtx-elf-csi-driver` PVC 保持 Bound。
- 对象存储页面改为默认展示已有配置，并提供“新增 MinIO”和“对接其他 S3”两个入口；外部 S3 在写入、读取和 SHA-256 校验通过后才保存 Profile。迁移策略的存储映射、镜像仓库映射和 Manifest 转换页面已拆分，并通过真实浏览器连续点击验证。
- 最终归档大小 608 MiB，包含 282 个清单文件，内部校验和外部 SHA-256 sidecar 均验证通过；归档 SHA-256 为 `baa7afb4e037d0967e4bc3c2486379a6648039568f2857afc5ffaa689b5bdece`。

## 真实 `sida → mw` 验收结果

| 场景 | 结果 | 关键证据 |
|---|---|---|
| 双集群控制链 | 通过 | 资源在两端落地；源 Deployment 停止并恢复；目标 ConfigMap/Secret 一致；14.05 秒 |
| 现有 Velero 元数据迁移 | 通过 | Backup `5/5`、Restore `5/5`；31.651 秒 |
| SmartX RWO 块 PVC FSB | 通过 | `smtx-elf-csi-driver → smtx-elf-csi-driver`；Backup `15/15`、Restore `5/5`、PodVolumeBackup `1`；文件内容一致；69.483 秒 |
| RWX NFS PVC FSB | 通过 | `nfs-csi → sks-migration-nfs`；Backup `14/14`、Restore `5/5`、PodVolumeBackup `1`；文件内容一致；67.398 秒 |
| `sida` CSI Data Mover 只读能力 | 通过 | SmartX 快照驱动、EnableCSI、DataUpload/DataDownload、node-agent 2/2 均 Ready |
| Compose named volume Kopia | 通过 | `.51` 源端 29 B 预同步、60 B 停机最终快照；`.28/sida → .30/sks` Registry 映射；恢复到 `smtx-elf-csi-driver` PVC；目标文件一致；源端回滚通过；93.97 秒 |
| 平台本体部署 | 通过 | API/Worker/Web/PostgreSQL 4/4 Ready；20 GiB SmartX RWO PVC Bound；NodePort 登录与会话读取通过；61.35 秒 |
| 最终离线包全新一键安装 | 通过 | 仅最小必填输入；16/16 OCI 导入；自动发现 ELF CSI；自动生成凭据；20 GiB PVC Bound；4/4 Running；首次登录与会话读取通过 |

实测还验证了两个故障路径：`restricted` PSA 会拒绝 Velero 1.13 的 restore helper；多个 StorageClass 映射 ConfigMap 会使 Restore `PartiallyFailed`。生产执行器现已针对两者修复并由同一真实场景复测通过。

## 可复现命令

```bash
make test
make lint
make build
make kind-e2e
```

真实控制链与现有 Velero：

```bash
make sks-control-e2e \
  TEST_SKS_SOURCE_KUBECONFIG=/absolute/path/to/sida.yaml \
  TEST_SKS_TARGET_KUBECONFIG=/absolute/path/to/mw.yaml \
  TEST_SKS_WORKLOAD_IMAGE=harbor.example/nginx:tag \
  TEST_SKS_WORKLOAD_PORT=8080

make sks-existing-velero-e2e \
  TEST_SKS_SOURCE_KUBECONFIG=/absolute/path/to/sida.yaml \
  TEST_SKS_TARGET_KUBECONFIG=/absolute/path/to/mw.yaml \
  TEST_SKS_EXISTING_VELERO_BSL=migration

make sks-fsb-e2e \
  TEST_SKS_SOURCE_KUBECONFIG=/absolute/path/to/sida.yaml \
  TEST_SKS_TARGET_KUBECONFIG=/absolute/path/to/mw.yaml \
  TEST_SKS_EXISTING_VELERO_BSL=migration \
  TEST_SKS_FSB_SOURCE_SC=smtx-elf-csi-driver \
  TEST_SKS_FSB_TARGET_SC=smtx-elf-csi-driver \
  TEST_SKS_FSB_IMAGE=harbor.example/nginx:tag

make sks-compose-e2e \
  TEST_SKS_TARGET_KUBECONFIG=/absolute/path/to/mw.yaml \
  TEST_COMPOSE_SSH_ENDPOINT=ssh://compose.example \
  TEST_COMPOSE_SSH_USERNAME=root \
  TEST_COMPOSE_SSH_PASSWORD="$TEST_COMPOSE_SSH_PASSWORD" \
  TEST_COMPOSE_SSH_HOST_KEY_FINGERPRINT=SHA256:... \
  TEST_COMPOSE_SOURCE_KOPIA_IMAGE=source-harbor/project/kopia@sha256:... \
  TEST_COMPOSE_KOMPOSE_IMAGE=target-harbor/project/kompose@sha256:... \
  TEST_COMPOSE_KOPIA_IMAGE=target-harbor/project/kopia@sha256:...

make sks-platform-acceptance \
  TEST_SKS_KUBECONFIG=/absolute/path/to/mw.yaml \
  TEST_PLATFORM_ADMIN_PASSWORD_FILE=/absolute/path/to/admin-password \
  TEST_PLATFORM_MASTER_KEY_FILE=/absolute/path/to/master-key
```

对于外部已有且源端设置为 `ReadOnly` 的 BSL，实测前必须临时改为 `ReadWrite`，并在命令退出时恢复；平台“复用已有 Velero”流程会自动按源/目标角色创建正确访问模式的迁移专用 BSL。

## 发行边界

- 本次一次性 AMD64 工具交付已经完成；镜像证据 marker 明确标记签名和漏洞扫描不作为阻断，不应把该包重新标注为需要安全审计的正式通用发行版。
- 安装器已证明只从解压包向内网 Harbor 导入镜像并由 SKS 从 Harbor 拉取。尚未另外搭建物理断网实验室重复相同动作。
- 本地双 Kind 入口仍保留作 CI 回归；真实 `sida → mw` 已执行更高价值的同一控制链测试并通过。

在当前已锁定的一次性工具、AMD64、SmartX SKS 和无需安全审计边界内，主链路、两类真实迁移、平台部署与完整离线交付均已闭环。
