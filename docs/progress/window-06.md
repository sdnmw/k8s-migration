# 窗口 06：Kubernetes 环境接入

- 状态：完成
- 日期：2026-09-02
- 门禁：fake client 与 TLS 测试集群接入通过

## 已完成能力

- 实现 Kubernetes 源集群与 SKS 工作负载集群的环境列表、创建、详情、删除和连接测试 API。
- 目标环境固定为 Kubernetes；系统不接入 CAPI 管控集群，也不调用 SKS 私有 API。
- 从 kubeconfig 当前 context 自动提取 Kubernetes API Server，不接受前端伪造 endpoint。
- kubeconfig 在写入环境记录前通过窗口 4 的 envelope encryption 存入凭证库；API 响应不包含原文或 `credentialId`。
- 创建环境失败时补偿删除已写入的凭证；删除环境后同步删除其专用凭证。
- 使用 `client-go v0.36.2` 建立 Kubernetes 客户端，匹配 Kubernetes 1.36 系列版本号规则。
- 连接测试独立返回 API Server、Authentication、Kubernetes Version、Namespace Count、Node Count 和 TLS 校验结果。
- 连接状态与基础能力快照持久化到 PostgreSQL；连接失败只记录分类信息，不持久化底层 API 错误正文。
- 环境写操作接入登录、CSRF 和审计链路。

## kubeconfig 安全边界

- 单次导入上限 1 MiB。
- 拒绝 `exec` 认证插件与旧式 `auth-provider` 插件。
- 拒绝本机 CA、客户端证书、私钥和 Token 文件引用，只允许内嵌数据。
- 拒绝 kubeconfig 代理地址，避免通过导入内容改变服务端请求路径。
- Kubernetes API 必须是无 userinfo 的 HTTPS URL。
- 允许 `insecure-skip-tls-verify` 以兼容离线测试集群，但连接结果明确显示 WARNING。

## 前端

- 将源环境与 SmartX SKS 两个 Mock 页面替换为真实环境 API。
- 支持选择本地 kubeconfig 文件或粘贴内容；提交成功后立即清空表单。
- 列表展示 API Server、Kubernetes 版本、节点数、命名空间数和连接状态。
- 支持连接测试、删除确认与错误提示；测试结果可逐项查看。
- 开发预览数据只存在于 `import.meta.env.DEV` 路由，不进入生产业务流程。

## 自动化与集成验证

- TLS `httptest` Kubernetes API：Bearer Token、版本、命名空间和节点读取通过。
- 恶意 kubeconfig：`exec` 与本机 Token 文件引用拒绝测试通过。
- Kubernetes 403 错误：结构化、脱敏连接结果测试通过。
- 服务补偿、能力持久化、API write-only、登录/CSRF/审计测试通过。
- PostgreSQL 18 集成：迁移、密文落盘、解密连接与凭证联删通过。
- `go test -race`：Kubernetes Adapter、Environment Service、API 全部通过。
- `make test`、`make lint`、`make build`：通过。

## 浏览器验收

- 1440×900 与 1280×900 均无页面水平溢出。
- Sidebar 216px，Header 56px，环境表格数据行 48px。
- 1280px 下 API Server、状态和操作列均完整可用。
- 导入弹窗字段、加密提示、文件选择与粘贴区域完整可见。
- 浏览器控制台无 error 或 warning。

## 已知边界

- 本窗口只采集连接测试所需的基础版本与计数；StorageClass、CSI、快照、Ingress、架构和容量的完整发现属于窗口 7。
- Docker Compose 主机注册属于窗口 8。
- OpenAPI lint 仍有 28 条既有的未来端点 4xx 响应建议，不阻塞合同有效性；这些端点会在对应窗口实现时收敛。
