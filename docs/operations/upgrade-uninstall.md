# 升级与卸载

## 平台升级

1. 校验新离线包的 manifest、镜像 digest 与 Chart 版本。
2. 备份 PostgreSQL PVC，并确认没有处于 `QUIESCE` 至 `ROLLING_BACK` 的任务。
3. 使用相同 release name 执行 Helm upgrade。数据库迁移在 API/Worker 启动时幂等执行。
4. 检查 `/readyz`、API `/metrics`、Worker metrics 和页面登录。
5. 执行一个只读能力发现及一个无数据测试迁移。

## Velero 升级与卸载

- 再次调用安装接口会由 Helm SDK 走原地 upgrade，版本必须与离线锁一致。
- 卸载前必须确保迁移任务已终止并结束回滚窗口，先调用 run cleanup 清理 Velero Backup/Restore。
- 卸载接口只删除受管的 `sks-migration-velero` Helm release；MinIO PVC、Bucket 数据与云凭证 Secret 默认保留。
- 非 Helm 管理或 release name 不匹配的已有 Velero 不会被接管或覆盖。可在对象存储页选择“复用已有 Velero”：系统只验证现有 Server/node-agent，创建迁移专用凭证 Secret 和 `migration-minio` BSL，不改 Deployment/DaemonSet。
- 复用模式下源环境 BSL 使用 `ReadWrite`，目标环境使用 `ReadOnly`；两边必须指向同一 Bucket 和 Prefix。外部管理的 Velero 不显示卸载按钮。
- FSB 恢复前，执行器会为目标迁移 Namespace 设置 `baseline` Pod Security，以兼容 Velero 1.13 的 `restore-wait` 辅助容器；评估仍负责阻止不满足目标安全约束的业务工作负载。
- StorageClass 映射使用整个 `velero` Namespace 唯一的共享 ConfigMap。各迁移映射按 run 注解合并；相同源 StorageClass 映射到不同目标时直接报冲突，避免 Velero 插件因多个配置对象而使恢复部分失败。
- 备份清理在源端提交 Velero `DeleteBackupRequest`，等待控制器删除对象存储内容；目标端只删除同步 Backup CR 和 Restore CR。
