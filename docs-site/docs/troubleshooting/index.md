---
title: 常见故障排查
sidebar_position: 1
---

# 常见故障排查

## 安装

### bundle file inventory does not match manifest

离线包解压后的文件被修改、缺失或多出文件。重新从 Release 获取并校验完整包；不要把个别新文件覆盖进旧目录后继续执行。

### 对象存储初始化 400

确认目标集群已在平台数据库中处于 CONNECTED，API 已完成迁移并可读取目标能力。容器 Ready 但初始化失败不算完整部署，应修正后重新执行幂等部署脚本。

## 应用发现

### Compose 只发现部分项目

在源主机用相同 SSH 用户执行 `docker compose ls -a`，核对 Config Files 是否可读。检查 `.env`、include、config、secret 和目录 ACL。平台不会把“发现 0 个应用”显示为成功而不解释原因。

### Kubernetes 应用发现请求失败

检查环境 ID、Namespace、kubeconfig 权限和 API 网络。按 Namespace 手选时，必须提交当前环境 ID 和有效 Namespace；页面应保留已选资源并展示具体错误码。

## 镜像

Compose 本地镜像和公有镜像都会同步到联动 Harbor。创建应用专属项目失败时回退到部署配置项目或 `library`。目标出现 ImagePullBackOff 时检查最终镜像地址、Harbor 证书、项目可见性和节点凭证。

## 数据与验证

### Traefik docker.sock 找不到 PVC

`docker.sock` 是 Docker 控制接口，不是可复制的数据。自 v0.5.1 起，支持的 Traefik Docker HTTP 标签路由会在目标端转为离线 File provider，并通过 Kubernetes Service 访问后端；socket 不会创建 PVC 或进入 Kopia。业务卷和证书卷仍正常迁移，源 Compose/环境配置保持不变。

要求显式开启 Docker provider、`exposedByDefault=false`，并明确配置 HTTP router。当前支持 HTTP router、TLS certResolver、后端协议/端口和常用 Headers 中间件。不支持的 socket 消费者、TCP/UDP、其他中间件、静态配置冲突或不明确的后端端口会给出具体阻断原因；不要补空 PVC 或挂载目标节点的 Docker socket。

原 Host/域名匹配规则保留。验证目标入口时使用相应 Host/域名，实际 DNS/VIP 切流由管理员执行；只换目标 IP 不代表域名规则会自动改写。

### Pod 正常但任务仍失败

打开红色资源的下方详情，比较“执行时结论”和“当前观察”。如果执行时因镜像或探针失败，但当前已经恢复，重新验证会刷新当前观察；新 Run 应依据最新验证得到正确结论，历史失败 Run 保持不变。

### 数据存在但卷显示失败

检查目标 PVC 是否实际挂载、FSB/Kopia 传输记录、文件路径与校验值。仅看到数据库目录或共享 NFS 数据不代表发生了数据复制。

## 源端恢复

恢复失败时查看被恢复对象、期望副本数/Compose 状态、执行用户权限和当前资源冲突。修复后可再次执行恢复；“迁移成功，源端恢复失败”属于需要处理，不会伪装成完全成功。
