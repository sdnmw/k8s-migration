# 窗口 17：单实例 MinIO、S3 探测与官方镜像锁

- 状态：完成（含真实 SmartX SKS 验收）
- 日期：2026-09-04
- 已接受边界：一次性迁移工具使用官方 Community 镜像的已知风险

## 已完成实现

- 固定官方 `docker.io/minio/minio:RELEASE.2025-09-07T16-13-09Z` multi-platform index digest，记录对应源码 commit、AGPL 义务、已知 findings 和操作者风险接受。
- Bootstrap 服务拒绝任何与策略 digest 不一致的镜像；镜像导入客户 Harbor 后允许 repository 改写，但内容 digest 不变。
- 新增单实例 MinIO Helm Chart：一个 StatefulSet、一个 RWO PVC、实际发现的 SmartX 块 StorageClass、TLS、digest 镜像、非 root、只读根文件系统和 PVC Retain。
- 新增 ObjectStorageProfile / AddonInstallation PostgreSQL Repository、OpenAPI 契约与受认证 API。
- 新增完整 Bootstrap 服务：验证目标 SKS 工作负载集群和 SmartX ELF CSI，创建 restricted 托管命名空间，将随机 S3 凭证 envelope encryption 保存并写入 Kubernetes Secret，通过 Helm SDK 安装，创建 Bucket，执行随机对象 SHA-256 写读探测，重启 StatefulSet 后再次读取并清理探测对象。
- API 和数据库只暴露凭证引用，不回显 Access Key、Secret Key、kubeconfig 或 CA 私密材料；失败状态仅保存阶段化脱敏消息。

## 自动化验证

- MinIO Chart 渲染测试验证单副本、RWO、指定 StorageClass、TLS、digest、Restricted 兼容安全上下文和 PVC Retain；占位镜像或空 StorageClass 会渲染失败。
- 服务测试覆盖阻断策略、显式接受风险、官方 digest 不匹配时零外部写入、成功安装全步骤和 S3 失败状态脱敏。
- Kubernetes 单元测试覆盖 StatefulSet 完整 revision 收敛和 Secret 数据深拷贝；S3 配置测试覆盖 HTTPS、TLS verify、非法 CA、path 和 userinfo 拒绝。
- PostgreSQL 18 集成回归验证 Profile、Add-on JSONB/upsert 与外键；修复取消后立即回滚使用 API 主机时间导致 Worker 在轻微时钟偏差下暂时无法领取任务的问题。
- `GOTOOLCHAIN=local GOPROXY=off make test lint build`、关键包 race 测试及 PostgreSQL 集成包三次重复执行通过。

## 真实 SmartX SKS 验收

- 目标工作负载集群：`mw`，Kubernetes v1.27.16，amd64，两个 Ready 节点。
- 能力发现：默认 StorageClass 为 `smtx-elf-csi-driver`，真实 provisioner/CSIDriver 为 `com.smartx.elf-csi-driver`；支持扩容并存在同驱动 VolumeSnapshotClass。
- 修正了仅按 StorageClass 名误判 CSI provisioner 的实现，同时通过命名空间和 Pod 注解关闭 SKS 的 k8tz hostPath 注入，保持托管命名空间的 restricted Pod Security 门禁。
- 目标部署使用锁定的官方 multi-platform index digest，经 DaoCloud 镜像代理拉取；运行镜像仍以 digest 固定，不依赖 tag 漂移。
- Helm SDK 创建 `sks-migration-minio` 单副本 StatefulSet；Pod 达到 `1/1 Ready`。
- PVC `data-sks-migration-minio-0` 为 `20Gi`、`RWO`、`Bound`，StorageClass 为 `smtx-elf-csi-driver`。
- S3 TLS NodePort 探测完成 Bucket 创建、随机对象写入、读取及 SHA-256 校验；随后滚动重启 StatefulSet，重启后再次读取相同对象并通过校验，最后清理探测对象。
- 新增 opt-in 真实验收测试，可通过 `TEST_SKS_KUBECONFIG=/path/to/config make sks-minio-acceptance` 重复运行；测试不打印 kubeconfig、Access Key、Secret Key 或 TLS 私钥。

## 验收后保留资源

MinIO release、TLS/凭证 Secret、NodePort Service 和 RWO PVC 保留在 `sks-migration-system`，供窗口 18 至 24 的 NFS、Velero、Kopia 与真实迁移链路继续使用。验收生成的临时探测对象已删除。
