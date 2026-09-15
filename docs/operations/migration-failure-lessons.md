# 迁移失败经验与内置防护

本清单来自 `sida → mw` Kubernetes 资源迁移和 Harbor Docker Compose 迁移的实际故障。对应防护已经进入 API、Worker、Web 和离线发行包，不需要在目标环境手工打补丁。

## Docker Compose / Harbor

- **Compose 项目漏发现**：发现同时读取 `docker compose ls --all --format json`，运行和已停止项目均纳入；每个项目独立读取和解析，单个失效项目不会再导致同一主机的正常项目全部消失。
- **`.env`、`env_file` 无权读取**：优先由远端 Docker Compose 在源主机执行 `config` 生成规范化定义；普通用户失败时，仅对固定只读命令尝试现有 `sudo -n`。因此无需平台自动执行永久 `setfacl`，也不会静默修改源目录权限。
- **主机指纹阻塞首次导入**：指纹改为可选，首次连接采用 TOFU 保存主机密钥，后续连接发现变化会阻止执行。
- **Harbor 文件挂载恢复错误**：区分 bind file 与 bind directory；文件恢复为 PVC `subPath`，同一源目录的共享挂载合并为一个 PVC，避免重复拷贝和挂载冲突。
- **目标服务启动顺序卡住**：Compose 的 `ports` 和 `expose` 均转换为集群内 Service；所有 Deployment 先统一恢复副本，再等待 Ready，避免按服务串行等待依赖造成死锁。
- **Kopia 重启后丢失上下文**：快照和恢复 helper 使用确定性名称，Worker 重启后重新连接既有任务；卷容量依据实际快照估算，已扩容 PVC 不缩容。
- **源镜像在目标集群不可拉取**：本地 build 镜像和公有镜像统一在 Preflight 镜像化到安装关联 Harbor 的应用公开项目；创建项目无权限时自动降级到 `library`，并保留事件证据。源 compose 定义不被修改，目标清单只使用镜像化后的地址。

## Kubernetes

- **手选资源保存失败**：API 只接收 `apiVersion/kind/namespace/name` 稳定标识，Inventory 展示字段不会再被错误提交给严格解码器。
- **无卷清单停在最终备份或误停源端**：`VolumeMode=NONE` 直接从最终备份进入资源转换，不执行 Velero 数据恢复，也不缩容源工作负载。
- **Velero 恢复被运行时 Pod 拦截**：资源恢复排除 Pod、ReplicaSet 和 Event，目标控制器重新生成运行时对象，避免源集群注入字段污染目标。
- **有卷迁移恢复副本不完整**：停机前持久化 Deployment/StatefulSet 副本快照；取消、验证失败或人工恢复源端时按快照恢复，并保留目标资源用于诊断。

## 操作与诊断

- 任务可在终态执行“重新执行”“编辑任务”“删除任务”；删除为软归档，不删除业务或证据。
- 完成切流后仍可执行“恢复源端”；动作只恢复源工作负载，不自动切换 DNS/LB，不删除目标资源。
- Worker 心跳、外部对象等待、重试原因和逐资源结果写入持久化时序；重复证据提示在展示层去重。
