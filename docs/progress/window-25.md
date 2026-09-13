# Window 25 — 可观测性、生命周期与故障回归

## 结果

Window 25 工程门禁已通过。平台和迁移辅助任务均具备资源边界；默认 Helm 部署生成 NetworkPolicy；API 与 Worker 均输出 Prometheus 文本指标；迁移报告增加可直接审阅的结果摘要；终态任务可按精确 run-id 标签清理 Velero Backup/Restore；受管 Velero 支持 Helm 原地升级与幂等卸载。

## 已完成

- API `/metrics` 输出按 HTTP 方法、ServeMux 路由模板和状态码聚合的请求数、耗时与并发请求数，避免把 UUID 放入指标标签。
- Worker `:9090/` 输出按有限 StepType 与固定 outcome 聚合的成功、重试、失败、租约丢失和存储错误指标。
- Helm Chart 增加 API/Worker metrics Service，并可选创建两套 ServiceMonitor。
- 报告摘要包含任务结果、总时长、步骤成功/失败数、告警/错误数、迁移字节、平均吞吐、切流确认和源端回滚标记。
- 新增 `POST /migration-runs/{runId}/cleanup`。仅终态 Run 可执行；Kubernetes 路径分别在源端和目标端按精确 `migration.smartx.com/run-id` 删除 Restore 与 Backup，其他 Run 的对象不受影响。
- Compose Kopia 快照默认保留，清理响应明确返回保留项，避免在回滚窗口内误删仓库快照。
- 新增 `DELETE /addons/{environmentId}/velero`。只卸载受管 Helm release，保留 MinIO 数据与凭证 Secret；重复卸载返回已移除状态。
- Helm SDK 继续以锁定 Chart 版本决定 install/upgrade 分支；升级启用 atomic、wait、wait-for-jobs 和 cleanup-on-fail。
- API、Worker、Web、PostgreSQL 原有资源 requests/limits 已通过 Chart；Kompose、Kopia Restore、Endpoint Validation 和 PVC staging 也增加 CPU/内存 requests/limits。
- 默认 NetworkPolicy 拒绝无授权入口；仅开放 Web、API、Worker metrics 与 PostgreSQL 必要流量。API/Worker 为访问导入集群、MinIO、SSH 与 Registry 保留广域出站能力。

## 故障注入回归

- Worker 在 PostgreSQL Claim 首次失败后继续轮询并恢复处理，不退出也不遗失任务。
- Worker 租约心跳丢失会取消执行上下文，且不会由失去租约的 Worker 写入完成/重试状态。
- 达到最大尝试次数后任务进入失败；暂时性失败进入延时重试。
- Endpoint Validation Job 失败由执行器返回错误，状态机进入已有自动回滚链路。
- Velero 清理测试验证只删除精确 run-id 的 Backup/Restore，保留其他任务的备份。
- Helm 测试覆盖首次安装、原地升级、卸载、锁定版本不一致与 Chart 路径逃逸。

## 真实 SKS 只读验证

- `sida` kubeconfig 已保存到 Git 忽略目录，权限为 `0600`。
- `sida` 为 Kubernetes v1.27.16，3 个节点全部 Ready，默认 StorageClass 为 `smtx-elf-csi-driver`，另有 `nfs-csi`。
- `sida` CSI Data Mover 只读验收通过：SmartX VolumeSnapshotClass 驱动匹配、EnableCSI 开启、DataUpload/DataDownload API 存在、node-agent 2/2、Backup/Restore 数据移动路径均 Ready。
- 只读检查发现 `sida` 与 `mw` 当前都运行非 Helm 管理的 Velero v1.13.2，且已有可用 BSL。系统按既定边界不会覆盖该安装，因此 Window 26 真正迁移前必须先确定复用、隔离安装或升级接管方案。

## 可复现验证

- `.cache/toolchains/go/bin/go test ./cmd/... ./internal/...`
- `npm --prefix web run test`
- `make lint`
- `make build`
- `TEST_SKS_KUBECONFIG=/absolute/path/to/sida.yaml .cache/toolchains/go/bin/go test -v ./internal/acceptance -run '^TestCSIDataMoverCapabilitiesOnSKSReadOnly$' -count=1 -timeout=3m`

## 下一窗口

Window 26 进入最终双集群 E2E、真实 `sida → mw` 验收、性能记录、UI 人工校准与签名离线发行。现阶段硬阻塞是两端 Velero 1.13.2 的处置、目标 Harbor 地址/凭证与可停机测试 Namespace；Compose 链路还需要 SSH 测试主机。
