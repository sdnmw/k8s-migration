# Window 27 — 应用发现、精确资源选择与评估诊断

## 结果

本窗口补齐 Docker Compose 应用/能力发现、Kubernetes 自动与手动两种应用选择模式、可下钻的迁移评估细则和可操作的凭证管理页。手动选择不是前端筛选：运行时会给所选资源和临时 PVC staging Pod 加本次 Run 的选择标签，Velero Backup 使用相同 LabelSelector，停源业务也只缩容所选工作负载。

正式 `mw` 平台已通过最终 AMD64 包升级到 Helm revision 9，API、Worker、Web 和 PostgreSQL 全部 Ready。升级安装器已修复“管理员修改密码后仍使用安装时密码登记默认 MinIO”的误报：首次安装仍自动登记；升级只复用现有 MinIO 和对象存储记录，不依赖管理员密码。

## 本窗口完成

- Compose 源环境新增“发现应用”和“能力发现”。应用发现执行受控的 `docker compose ls --all --format json`，校验项目名与绝对 Compose 文件路径，再执行 `docker compose config --no-interpolate` 保存标准化 Inventory。
- Compose 能力快照新增 Docker Root、存储驱动、架构、操作系统、CPU、内存、Cgroup Driver 和 Compose 项目数。
- Kubernetes 应用发现支持“扫描全部 Namespace”，只登记包含工作负载、Service、Ingress 或 PVC 的 Namespace，不再限制为预先登记的三个应用。
- 创建迁移支持“按 Namespace 手选资源”：读取 Namespace Inventory 后可精确勾选 Deployment、StatefulSet、Service、Ingress、PVC、ConfigMap、Secret、RBAC 和 CR 等对象，并保存为独立迁移应用。
- 精确选择贯通到执行器：选中资源被临时标记，本次 Velero Backup 使用 LabelSelector；PVC staging Pod 使用同一标签；停源阶段只处理所选 Deployment/StatefulSet。
- 迁移评估的兼容性得分、BLOCKER 和 WARNING 三张卡片均可点击，抽屉显示规则 ID、类别、受影响资源、判定原因和处理建议。
- 凭证页替换原占位页：支持列出仅含元数据的 kubeconfig、SSH、Registry 和 S3 凭证，新增凭证内容只写入且不可回显，并支持删除未被引用的凭证。
- OpenAPI 增加应用预览、全 Namespace 发现、Compose 自动发现和凭证 CRUD 契约。
- 一键安装器在升级前检测既有平台；既有平台不再用可能已经失效的初始管理员密码重复登录。

## 实机证据

- `192.168.112.51` 返回 Compose 项目 `openclaw` 和 `sks-nacos-demo`，后者配置文件为 `/opt/sks-migration-demo-build-20260906/compose-nacos-springboot/compose.yaml`；标准化 Compose 配置命令执行成功。
- Compose 主机能力：Docker Root `/var/lib/docker`、存储驱动 `overlay2`、架构 `x86_64`、Rocky Linux 9.4、12 CPU、约 31 GiB 内存、Cgroup Driver `systemd`。
- `sida` 当前可读取 23 个非 Kubernetes 核心 Namespace；`demo-postgrest` Inventory 包含两个 Deployment、两个 Service、一个 PVC、ConfigMap 和 Secret，可由手选模式精确组合。
- `mw` 平台 Web 容器内已确认包含“从 Compose 主机发现应用”“按 Namespace 手选资源”“BLOCKER · 查看原因”和“添加凭证”代码。
- 最终一键部署从离线 OCI Layout 校验并导入 16 个 AMD64 镜像，自动发现 `smtx-elf-csi-driver`，复用默认 MinIO，Helm revision 9 成功完成。

## 测试门禁

- Go 全量测试：`go test ./cmd/... ./internal/...` 通过。
- Go Vet 与 gofmt 门禁通过。
- OpenAPI lint 通过且无规则错误。
- 前端 13 个交互测试通过，覆盖凭证弹窗、Kubernetes 两种选择模式和评估细则抽屉。
- TypeScript、ESLint 和生产构建通过。
- 最终离线包清单验证通过，共 282 个文件。

## 最终离线包

`output/sks-migration-center-0.1.0-amd64-discovery-final3/sks-migration-center-0.1.0-linux-amd64.tar.gz`

大小为 609 MiB，SHA-256 为 `6bab5a81025697eb6edecbb77de4403c5bcfe4c5cb2ab2f798b377932e57ca23`。

该归档内的 `deploy.sh` 仍只要求 Harbor 地址、项目、凭据文件和目标 SKS kubeconfig；实验室自签名 Harbor 需显式增加 `--insecure-registry`。
