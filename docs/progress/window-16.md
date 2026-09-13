# 窗口 16：离线包、Harbor 导入与 Helm SDK Add-on Manager

- 状态：完成
- 日期：2026-09-04
- 门禁：无公网依赖完成离线资产校验、Registry 导入适配器测试和基础 Chart 渲染

## 离线发行格式

- 新增严格 `OfflineImageLock`：每个镜像必须记录 source digest、目标 repository:tag、目标平台、OCI layout、SPDX SBOM、签名 bundle 和漏洞扫描报告。
- 打包前递归验证 OCI index/manifest/config/layer 的 digest 与 size，并强制 OCI 根 descriptor 与 source lock digest 一致。
- 缺项、未知字段、tag-only source、路径逃逸、符号链接、重复平台或不支持的平台均阻止发行。
- 生成确定性 tar.gz：文件排序、时间戳归零、权限归一；相同 staging 目录字节级一致。
- `bundle-manifest.json` 记录逐文件 SHA-256 与 size，`SHA256SUMS` 同时覆盖业务文件和 manifest 本身；篡改或尾随 JSON 都会失败。

## Registry v2 / Harbor 导入

- 新增独立 `offline` 二进制，提供 `pack`、`verify`、`import` 三个命令；打包拒绝覆盖已有输出，失败时移除未完成新文件。
- 导入器直接读取 OCI Image Layout 并调用 Registry v2 API，不依赖 Docker daemon、Skopeo 或 Crane。
- 导入前先校验整包；上传前再次验证 descriptor，已存在 blob 使用 HEAD 跳过，多架构 index 和 manifest 按 digest 推送后再写目标 tag。
- 支持 Basic 和同源 Bearer challenge；Registry、token realm 和 blob upload Location 禁止静默换源。默认只允许 HTTPS，`--insecure` 必须显式开启。
- Registry 用户名和密码只允许从文件读取，不进入进程参数；错误和 JSON 结果不输出凭证。

## Helm Go SDK Add-on Manager

- 固定 `helm.sh/helm/v3 v3.21.4`，API/Worker 直接调用 Go SDK，不执行 Helm CLI。
- kubeconfig 只在内存解析并在使用后清空调用方字节缓冲；release 存入 Kubernetes Secret。
- Chart 只能从离线 chart root 加载，拒绝绝对路径、路径逃逸和 symlink 逃逸；锁定版本必须匹配 Chart version 或 appVersion。
- 自动判定 install/upgrade；启用 atomic、wait、wait-for-jobs、upgrade cleanup-on-fail，并限制最长一小时、默认十五分钟。
- 新增基础平台 Chart，部署 API、Worker、Web、PostgreSQL；必须传入真实 StorageClass、外部 Secret 和四个 `repository@sha256` 镜像，任何默认占位值都会阻止渲染。

## 离线物料与可复现流程

- `deploy/offline/components.yaml` 列出平台、PostgreSQL、MinIO、Velero、S3 插件、NFS CSI sidecar、Kompose、Kopia 和迁移 helper 的完整发行清单及归属窗口。
- `images.lock.yaml.tmpl` 是发行 CI 输入，不冒充已锁定发行物；正式打包必须先替换所有变量、补齐全部组件和 OCI/SBOM/签名/扫描文件。
- `deploy/offline/README.md` 固化联网发行侧组装、隔离区校验、Harbor 导入和基础安装所需输入。
- Makefile 新增 `dist/offline` 构建及 `offline-pack`、`offline-verify` 目标；CI 增加 `GOPROXY=off` 离线包与 Add-on 测试。

## 验证

- `GOTOOLCHAIN=local GOPROXY=off make test lint build` 全部通过。
- Go 全仓测试、Go vet、gofmt、React 6 项测试、ESLint、OpenAPI 校验和三个后端二进制构建通过。
- `GOPROXY=off go test -race ./internal/offline ./internal/addon` 通过。
- 测试覆盖可复现归档、资产篡改、lock/OCI digest 不一致、Basic/Bearer Registry、Helm install/upgrade 分支、版本不匹配、symlink 逃逸、kubeconfig 清理和基础 Chart digest 门禁。
- OpenAPI 仍保留 13 条未来窗口占位端点缺少 4XX 的非阻塞 warning；没有新增契约错误。

## 已知边界

- 本窗口交付发行框架与基础平台 Chart，不伪造尚未选定的第三方镜像 digest。最终 `images.lock.yaml` 只能在窗口 17 至 23 锁定 MinIO、NFS CSI、Velero、Kompose、Kopia 等实际版本并通过漏洞门禁后生成。
- 当前 Registry token service 限制为与 Registry endpoint 同源，符合目标 Harbor 部署并避免凭证被 challenge 引导到其他主机；若客户采用独立认证域，需要后续增加显式 allowlist，不能自动放宽。
- Chart 只创建最小平台 ServiceAccount 且不挂载集群 token。执行 Kubernetes/Velero 操作所需的按环境 kubeconfig 仍来自加密凭证库，不扩大平台 Pod 的默认 RBAC。
- MinIO 的具体交付方式和真实 S3 持久化属于窗口 17；最终按操作者风险接受决定改为固定官方 digest 镜像，不在窗口 16 冒充已交付。
