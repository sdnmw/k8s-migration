# ADR 0004：MinIO 官方镜像与一次性工具风险接受

- 状态：接受
- 日期：2026-09-04

## 背景

MinIO 官方发布的最后一个 Community 源码版本是 `RELEASE.2025-10-15T17-29-55Z`，但该版本没有对应官方容器镜像，官方发布说明要求容器用户自行从源码构建。官方 Docker Hub 的最后一个 `minio/minio` 镜像版本是 `RELEASE.2025-09-07T16-13-09Z`。

该工具仅在迁移期间部署一个内部 MinIO 作为 Velero/Kopia 中转仓库，不作为客户长期对象存储服务。操作者明确决定接受官方 Community 镜像的已知安全风险，不要求通过常规生产安全审计。

## 决策

- 使用官方 `docker.io/minio/minio:RELEASE.2025-09-07T16-13-09Z`。
- 固定 multi-platform index digest `sha256:14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e`，离线导入 Harbor 后仍以同一 digest 部署，不使用 `latest`。
- 保留官方源码 tag、完整 commit `07c3a429bfed433e49018cb0f78a52145d4bedeb`、AGPL 源码提供义务和已知 HIGH findings，状态记录为 `ACCEPTED_RISK`，而不是伪装为无漏洞的 `PASSED`。
- Bootstrap API 只接受策略中锁定的 digest；仓库地址可替换为客户 Harbor，digest 不得改变。
- 删除自建 MinIO 镜像配方，避免“官方镜像”和“本地源码镜像”同时存在导致离线物料来源不确定。
- 运行边界仍保持 TLS、非 root、只读根文件系统、单副本、RWO PVC 和独立托管命名空间。迁移结束后应卸载 MinIO，PVC 是否清理由操作者确认。

## 边界

风险接受只适用于此次迁移工具内的临时 MinIO，不自动扩展到平台自身、Velero、NFS CSI、客户业务镜像或其他长期服务。

## 参考

- [官方镜像版本与 index digest](https://hub.docker.com/layers/minio/minio/RELEASE.2025-09-07T16-13-09Z/images/sha256-a1a8bd4ac40ad7881a245bab97323e18f971e4d4cba2c2007ec1bedd21cbaba2)
- [官方源码 release](https://github.com/minio/minio/releases/tag/RELEASE.2025-09-07T16-13-09Z)

