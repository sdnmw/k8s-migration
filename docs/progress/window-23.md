# Window 23 — Compose Kopia 卷迁移

## 结果

Window 23 的工程门禁已通过。Docker Compose 的 named volume 与绝对路径 bind mount 已接入“在线预同步 → 停止源服务 → 最终增量快照 → 目标 PVC 恢复 → 启动目标工作负载”的 Worker 执行链；迁移失败或人工切流前取消时会在源主机执行受控的 `docker compose up -d`。

## 已完成

- 采用官方 Kopia 0.23.1 发布二进制构建 amd64/arm64 辅助镜像，校验官方下载包 SHA-256，并将源码锁加入离线组件目录和镜像锁模板。
- Compose Inventory 持久化 Docker volume 的实际 runtime name；未显式返回时按 Compose 项目规则推导。
- 源端 SSH 适配器只暴露固定动作：Compose stop/start 与 Kopia snapshot；不提供任意 Shell API。
- named volume 只读挂载进 Kopia 容器；bind mount 必须为绝对路径，并阻断 `/etc`、`/proc`、`/sys`、`/dev`、`/run` 等系统目录。
- Kopia Repository 自动连接目标 SKS 上已就绪 MinIO，路径隔离为 `compose/<application-id>/`。
- 预同步和最终快照 ID 持久化为 MigrationEvent；逐卷字节数写入统一 `VolumeTransfer`。
- 从 Kompose 输出解析服务 mountPath 到目标 PVC 的对应关系，覆盖 named volume 和 bind mount 生成的 PVC。
- 恢复前将生成的 Deployment/StatefulSet 设置为 0 副本；逐 PVC 完成 Kopia restore 后恢复原始副本。
- 目标恢复 Job 使用 Kubernetes Secret 注入 S3/Kopia 凭证，不把凭证写入 Job 明文字段。
- Worker 已装配 PostgreSQL PlatformRepository、SSH mover 和 digest 固定的 Kopia 镜像。
- 新增完整状态链单元测试，覆盖预同步、停机、最终快照、目标恢复、目标启动和源端回滚。

## 可复现验证

- 本机构建 `linux/arm64` Kopia 镜像成功，实际输出版本 `0.23.1`。
- 本地 Kopia 文件仓库实测：创建快照后按 snapshot ID 恢复到空目录，文件内容一致。
- 官方 Kompose 1.38.0 对包含 named volume 与 bind mount 的样例实际生成 Deployment 和两个 PVC；执行器可解析出 `redis-data` 与 `redis-claim1`。
- `make test` 通过。
- `make lint` 通过；OpenAPI 有 5 个既有 4XX response 警告，无错误。
- `make build` 通过，Go API/Worker/Offline CLI 与 React 生产构建全部成功。

## 尚未执行的真实验收

- 尚无可连接的 Docker Compose SSH 测试主机和测试业务，因此没有执行真实 Redis named volume 的端到端迁移。
- Kopia/Kompose 镜像尚未推送到目标 Harbor，真实 `mw` SKS Restore Job 未运行。
- `sida` kubeconfig 仍未落到工作区；这不阻塞 Compose 链路编码，但继续阻塞 Kubernetes `sida → mw` 的真实迁移验收。

## 下一窗口

Window 24 将完善 HTTP/TCP 应用验证、人工切流确认、主动回滚 API与页面操作，并把验证失败后的源端恢复闭环做成可测试门禁。
