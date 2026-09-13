# SKS Migration Center

SKS Migration Center 是面向 SmartX SKS 工作负载集群的 Kubernetes 与 Docker Compose 应用迁移工作台。产品规格位于 [`sks-migration-center-spec`](./sks-migration-center-spec)。

## 当前状态

可上线 v1 建设范围已完成。工程实现、Kubernetes 主链路、Docker Compose 主链路、平台本体真实部署和一次性 AMD64 离线发行验收均已通过。真实 `mw` 目标、`sida` 源集群 kubeconfig 及外部 Compose SSH 主机均已接入。现有能力包括 API/数据库契约、SmartX 风格管理界面、Kubernetes 与 Compose 源端发现、能力快照、Namespace Inventory、评估和 BLOCKER 门禁、资源映射、Transform Engine、六步向导、任务编排与 SSE、离线交付、默认 MinIO 或外部 S3、外部 NFS、Velero FSB、CSI Snapshot Data Mover、隔离 Kompose、Compose Kopia 卷迁移、目标业务验证、人工切流/回滚闭环、资源拓扑与离线 HTML 诊断报告。

平台现已部署在真实 `mw` SKS 工作负载集群：API、Worker、Web 和 PostgreSQL 4/4 Ready，PostgreSQL 使用 `smtx-elf-csi-driver` 的 20 GiB RWO PVC，NodePort 管理界面为 <http://192.168.118.206:31500>。管理员登录和会话 API 已通过真实链路验证；密码保存在本地 Git 忽略的 Secret 文件中，不写入文档。

完整一次性离线包通过 GitHub Release 发布，不进入 Git 历史。发行包包含 16 个 `linux/amd64` OCI 镜像、全部 Helm Chart、静态 Linux AMD64 安装器和部署说明。解压后执行 `deploy.sh`，只需提供 Harbor 地址、项目、凭据文件和目标 SKS kubeconfig；详细参数见 [`离线部署说明`](./deploy/offline/README.md)。

窗口 17 已在真实 `mw` SKS 工作负载集群完成。单实例 MinIO 使用锁定官方镜像和 `smtx-elf-csi-driver` RWO PVC，已通过 TLS S3 写读、SHA-256 校验、StatefulSet 重启及重启后持久化校验。按一次性迁移工具的使用边界，官方 OSS 镜像的已知风险已显式接受。详见 [`窗口 17 报告`](./docs/progress/window-17.md)。

窗口 18 已提供外部 NFS Profile、离线 NFS CSI 4.13.4 Chart、Harbor digest镜像配置、Retain StorageClass和 Web 管理入口。真实 `mw` 集群已创建 `sks-migration-nfs`，并通过动态 PVC 写入、卸载、独立 Pod重新挂载读取和清理验证。详见 [`窗口 18 报告`](./docs/progress/window-18.md)。

窗口 19 已提供官方 Velero 1.18.1 Chart、AWS 插件 1.14.0、node-agent/Kopia、BSL与 Backup CR客户端、对象存储/Velero Web管理页及双 SKS验收入口。详见 [`窗口 19 报告`](./docs/progress/window-19.md)。

窗口 20 的 Kubernetes FSB 执行内核已完成：在线预同步、未挂载 PVC staging、源端副本快照与停机、最终 Backup、目标 Backup 同步、Namespace/StorageClass 映射 Restore、目标副本恢复、完整资源验证、逐 PVC 进度和失败回滚均已接入 Worker。详见 [`窗口 20 进度`](./docs/progress/window-20.md)。

窗口 21 已实现 CSI Snapshot/Data Mover能力矩阵、源 StorageClass与快照驱动精确匹配、DataUpload/DataDownload进度、Raw Block Linux门禁和 Worker实时复检。`mw` 只读探测确认 SmartX快照驱动、EnableCSI、DataUpload/DataDownload及 node-agent 2/2均可用；真实跨集群迁移尚未执行。详见 [`窗口 21 报告`](./docs/progress/window-21.md)。

窗口 22 已完成 Compose 应用自动发现与手动注册、隔离 Kompose Job、Transform 映射、Worker 源类型分流、目标 Server-Side Apply 和就绪验证，并使用官方 Kompose 1.38.0 完成真实链路验证。详见 [`窗口 22 报告`](./docs/progress/window-22.md)。

窗口 23 已完成 Compose named/bind volume 的 Kopia 在线预同步、停机最终快照、目标 PVC 恢复、目标副本启动和源端恢复闭环；官方 Kopia 0.23.1 已完成真实 Compose 主机到 `mw` 的快照恢复与数据校验。详见 [`窗口 23 报告`](./docs/progress/window-23.md)。

窗口 24 已完成 Workload/PVC/Service/Ingress 基础验证、目标集群内 HTTP/TCP 探测、人工切流确认、主动回滚、源端自动恢复和自包含 HTML 报告下载。详见 [`窗口 24 报告`](./docs/progress/window-24.md)。

窗口 25 已完成 API/Worker Prometheus 指标、报告摘要、终态 Velero 备份清理、受管 Velero 升级/卸载、平台与临时 Job 资源约束、NetworkPolicy 以及数据库短暂断连/租约丢失等故障回归。`sida` 只读探测确认 CSI Data Mover 上传路径已就绪。详见 [`窗口 25 报告`](./docs/progress/window-25.md)。

窗口 26 已新增双 Kind/真实双集群控制 E2E、离线包 Ed25519 签名、现有 Velero 复用、角色化 BSL、Velero 1.13 PSA 兼容、并发安全的唯一 StorageClass 映射、正确的 DeleteBackupRequest 清理，以及完全由真实 API 驱动的迁移列表和概览。真实 `sida → mw` 已完成元数据、RWO SmartX 块 PVC 与 RWX NFS PVC 的 Velero FSB 迁移及文件校验；真实 Compose 主机也已通过 Kompose、Registry 映射、Kopia 增量同步、SmartX 块 PVC恢复和回滚验收。详见 [`窗口 26 报告`](./docs/progress/window-26.md)。

## 本地要求

- Docker 24+
- Docker Compose v2
- 可选：Node.js 22+，用于不经容器运行前端命令
- Go 不要求安装在宿主机；`make` 默认通过固定版本容器执行 Go 命令

## 快速开始

```bash
cp .env.example .env
make bootstrap
make test
make build
make dev
```

`make dev` 会在被 Git 忽略的 `.data/secrets` 中生成随机开发管理员密码和 256 位凭证主密钥，并在终端显示开发密码。非开发环境必须通过 Kubernetes Secret 只读挂载这两个文件。TLS 部署应保持 `COOKIE_SECURE=true`；仅隔离网络内的一次性 HTTP 工具部署可显式设为 `false`。

启动后访问：

- Web: <http://localhost:3000>
- API 健康检查: <http://localhost:8080/healthz>
- API 就绪检查: <http://localhost:8080/readyz>
- API Prometheus 指标: <http://localhost:8080/metrics>
- Worker Prometheus 指标: <http://localhost:9090/>

停止本地栈：

```bash
make down
```

离线包组装、校验和 Harbor 导入说明见 [`deploy/offline/README.md`](./deploy/offline/README.md)。

## 工程结构

```text
cmd/                  API 与 Worker 入口
internal/             后端领域、应用和基础设施代码
web/                  React + TypeScript 前端
build/package/        容器镜像定义
deploy/               Kubernetes 与离线交付资产
docs/adr/              架构决策记录
sks-migration-center-spec/ 产品与 UI 规格
```
