# 窗口 22：Compose 注册、Kompose 转换与目标部署

- 状态：无状态链路工程实现完成；真实 SKS 部署验收待 Kompose 镜像导入 Harbor
- 日期：2026-09-05
- 当前门禁：代码、单元测试、OpenAPI、前端构建以及本地真实 Kompose 1.38.0 转换通过；目标 `mw` 尚未创建验收资源

## 已完成

- `/compose/analyze` 增加注册模式：上传内容绑定到已连接的 Docker Compose 源环境，完整 Compose 定义作为专用凭证保存，API 仅返回规范化 Inventory。
- `SourceApplication` 持久化 Compose 定义引用和结构化 Compose Inventory；重复注册同一项目时替换旧定义。
- 源环境页面改成“注册 Compose 应用”，必须选择已登记的 Compose 主机；注册后可直接进入评估和迁移向导。
- Worker 新增按 `SourceApplication.sourceType` 分流，Kubernetes 继续使用 Velero Executor，Compose 使用独立 Compose Executor。
- Kompose 在目标集群的 `sks-migration-system` 命名空间运行：
  - compose.yaml 和 `.env` 通过临时 Secret 只读挂载；
  - Pod 不挂载 ServiceAccount Token；
  - 关闭提权、丢弃 Linux capabilities、使用只读根文件系统；
  - init container 只负责转换，输出写入 EmptyDir；辅助容器只输出生成的 Manifest；
  - Job 和输入 Secret 在成功或失败后自动清理。
- Kompose 输出进入既有 Transform Engine，继续执行 Registry、Namespace、StorageClass、Ingress、NFS 和 NodeLabel 映射，并清理运行时字段。
- Transform Engine 新增内部执行渲染：预览继续脱敏 Secret，实际部署 Manifest 保留原始 Secret 数据。
- 目标部署使用 Kubernetes Dynamic Client 和 RESTMapper执行 Server-Side Apply；默认拒绝已有资源覆盖，只有计划明确允许时才 Force Apply。
- Compose 的目标 Namespace由项目名确定，也可用 Namespace Mapping显式覆盖；系统只允许部署命名空间级资源，不接受 Kompose输出之外的 Namespace/CRD扩权。
- 无状态 Compose 的预同步、停机、最终同步和传输步骤会记录为无数据操作；有 named volume或 bind mount 时明确阻断，等待窗口 23 Kopia 数据链路，避免静默丢卷。
- 加入基于官方 Kompose v1.38.0 release binary的 amd64/arm64镜像构建，版本、下载地址和 SHA-256写入离线 Source Lock；Helm和离线镜像锁模板均已接入 Kompose镜像。

## 验证

- Compose 注册测试覆盖环境类型、连接状态、定义持久化、旧凭证替换和响应不泄漏原始文件。
- Kompose资源测试覆盖镜像锁定、临时 Secret、无 Token、受限安全上下文、稳定 Job名称和受控命令。
- Apply解析测试覆盖命名空间级对象与 Namespace/CRD阻断。
- Compose Executor测试覆盖转换、Registry映射、目标 Namespace映射、Apply、验证、卷数据阻断和 Worker源类型分流。
- `make test`、`make lint`、`make build` 全部通过。
- 本机实际构建 `linux/arm64` Kompose镜像成功，二进制校验通过，运行结果为 `1.38.0 (a8f5d1cbd)`。
- 使用 `compose-stateless.yaml` 真实执行 `kompose convert --stdout`，得到 Service和 Deployment；未把 Namespace对象混入执行输入。

## 尚未通过的窗口门禁

- 还没有目标 Harbor地址和推送凭证，因此本地构建的 Kompose镜像尚未导入目标集群可访问的 Registry。
- 还没有在 `mw` 创建无状态 Compose验收资源。尝试复用已打开的 CloudTower页面下载 `sida` kubeconfig时，macOS已锁屏，浏览器自动化无法继续；没有绕过锁屏。
- 业务镜像当前完成引用映射和目标 Apply，Registry到Harbor的实际镜像复制仍需目标 Harbor配置后验收。
- named volume与 bind mount会继续 BLOCKER；窗口 23实现 Kopia预同步、停机增量同步和 PVC恢复。

## 上游依据

- Kompose版本和二进制 SHA-256来自官方 [v1.38.0 Release](https://github.com/kubernetes/kompose/releases/tag/v1.38.0)。
