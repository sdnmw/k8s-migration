# 故障注入与恢复检查表

仅对专用测试任务执行。目标业务验证失败前不确认切流；目标资源默认保留供诊断。

| 故障 | 注入点 | 预期结果 |
|---|---|---|
| PostgreSQL 短暂不可用 | Worker Claim/Heartbeat | Claim 失败后继续轮询；租约丢失时当前 Handler 被取消，由新租约恢复 |
| 源集群断连 | PreSync/FinalBackup | Step 重试；达到上限后失败；若已停机则调度 Rollback |
| MinIO 重启 | Velero/Kopia 上传 | 增量任务重试，不创建重复 Run；恢复后从持久化仓库继续 |
| 目标容量不足 | Restore/PVC Pending | 验证失败，恢复源 Deployment/StatefulSet 副本或 Compose 服务 |
| Velero PartiallyFailed | Backup/Restore 状态 | Step 失败并记录 CR 错误摘要，不进入人工切流 |
| HTTP/TCP 检查失败 | Validation Job | Run 进入 ROLLING_BACK，目标资源保留 |

清理仅在 Run 为 `COMPLETED`、`FAILED` 或 `CANCELLED` 且回滚窗口结束后执行。先下载 JSON 报告，再调用备份清理接口。
