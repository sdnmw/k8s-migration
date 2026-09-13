# 窗口 08：Docker Compose 主机与配置发现

- 状态：完成
- 日期：2026-09-03
- 门禁：示例 Compose 完整发现

## 已完成能力

- 支持把 Docker Compose 主机作为 `SOURCE/DOCKER_COMPOSE` 环境登记；目标环境继续强制为 Kubernetes。
- SSH 端点仅接受 `ssh://host[:port]`，禁止在 URL 中携带用户、路径或其他协议。
- 仅支持 SSH 私钥认证，并强制提交预先核对的 OpenSSH SHA-256 主机密钥指纹；不实现 TOFU。
- SSH 用户、私钥、可选私钥口令和主机指纹整体进入凭证 Vault，以 `SSH` 类型 envelope encryption 持久化；API 响应不包含凭证 ID 或密值。
- 连接探测仅能调用内部 `CommandID` 映射的 Docker Engine 与 Docker Compose v2 版本命令；没有接收原始命令或任意 Shell 的 API。
- SSH 命令输出限制为 64 KiB，网络连接、握手和命令执行均受 Context/Timeout 控制。
- 使用 Compose 官方 Go 参考实现解析上传的 `compose.yaml` 与可选 `.env`。
- Inventory 覆盖服务、镜像、build 标记、profiles、依赖、端口、网络、named volume、bind mount、环境变量键、configs、secrets 和禁用服务。
- `.env` 仅在内存中参与变量插值，结果只保留环境变量键；API、审计和 Inventory 均不返回密值。
- 拒绝 `include`、`env_file`、`label_file`、外部 `extends.file` 及本地 config/secret 文件引用，防止服务端读取任意路径。
- 对 bind mount、仅 build 服务和 privileged 容器生成明确风险提示。

## API 与前端

- `POST /api/v1/environments` 新增 Docker Compose 的 `endpoint` 与 write-only `ssh` 输入。
- `POST /api/v1/compose/analyze` 接收 multipart `compose`、可选 `environment` 和 `projectName`。
- OpenAPI 新增 SSH 输入、运行时能力、Compose Inventory、服务与资源 Schema。
- 源环境页可在 Kubernetes 与 Docker Compose 之间切换，上传私钥并录入固定主机指纹。
- 环境列表对 Compose 主机展示 Docker/Compose 版本，并隐藏仅适用于 Kubernetes 的能力发现动作。
- 新增 Compose 分析弹窗，展示服务、镜像、端口、挂载、环境变量键数量及迁移风险。

## 自动化与安全验证

- 进程内真实 SSH Server 验证 ed25519 私钥认证、固定主机密钥指纹、两条白名单命令及错误指纹拒绝。
- 验证未知 `CommandID` 无法执行。
- Compose 示例覆盖 profiles、依赖、端口、named volume、bind mount、网络和 `.env` 插值。
- 验证 `.env` 密码值不会出现在 JSON Inventory 或 API 响应。
- 验证服务器本地文件引用被稳定拒绝并返回 `COMPOSE_INVALID`。
- 验证 Compose 环境使用 `SSH` 凭证类型、连接状态与 Docker/Compose 版本快照可持久化。
- `go test -race`：SSH Adapter、Compose Analyzer、Environment Service、API 全部通过。
- `make test`、`make lint`、`make build`：通过。

## 浏览器验收

- 1280px 宽页面无水平溢出，环境表操作列完整。
- SSH 登记弹窗调整为 720px 双列布局，高度约 758px，可在 1280×900 内完整操作。
- Compose 分析弹窗在 1280×720 下约 820×510px，无水平或纵向溢出。
- Kubernetes/Compose 切换后字段、加密提示和可访问名称正确更新。

## 已知边界

- 本窗口只允许版本探测；窗口 22/23 才会加入同样基于白名单辅助程序的 Kompose 与 Kopia 执行能力。
- Compose 文件当前通过浏览器上传；从远端主机选择受控路径、允许 bind mount 根目录和停机 hook 将随迁移计划实现。
- `env_file` 必须由用户合并为单个上传的 `.env`；系统不会尝试从 API Server 或 SSH 主机读取任意文件。
- 镜像存在性、架构与 Registry 映射在 Assessment/Mapping 窗口完成。
