# MVP_TASKS.md

# 1. MVP 目标

完成两条端到端迁移链路：

1. Docker Compose → SmartX SKS
2. Kubernetes → SmartX SKS

重点是迁移工作流完整，而不是功能数量。

# 2. P0 必须实现

## Environment

- Kubernetes Source 添加
- SmartX SKS Target 添加
- Connection Test
- Kubeconfig 安全存储

## Docker Compose

- compose.yaml 上传
- `.env` 上传
- Service Discovery
- Volume Discovery
- Network Discovery
- Compose Assessment
- Kompose Conversion
- SKS Transformation
- Manifest Preview
- Deploy SKS

## Kubernetes

- Namespace Discovery
- Resource Inventory
- Assessment
- StorageClass Mapping
- Namespace Mapping
- IngressClass Mapping
- Velero Backup
- Velero Restore
- File System Backup
- PVC 进度展示

## Validation

- Deployment
- StatefulSet
- Pod
- PVC
- Service Endpoint
- HTTP Health Check
- TCP Check

## Migration

- MigrationPlan
- MigrationRun
- Step Progress
- Event Timeline
- Failure Message
- Migration Report

# 3. MVP 明确不做

Codex 不得擅自加入：

- 多租户
- SSO
- LDAP
- 复杂 RBAC
- 集群生命周期管理
- Helm 管理
- Kubernetes Dashboard
- VMware Migration
- KubeVirt
- Forklift
- 数据库增量同步
- MySQL Replication
- PostgreSQL Replication
- 不停机迁移
- DNS 自动切换
- LoadBalancer 自动切换
- 完整 Rollback
- Migration Schedule
- 多 Target 并发迁移
- Docker Image Build
- CI/CD
- GitOps
- Argo CD
- Service Mesh

# 4. Docker Compose Volume 限制

第一版对于：

- named volume
- bind mount
- database data

必须做到：

```text
识别
+
Assessment
+
风险提示
```

但不承诺自动迁移 Docker Volume。

示例：

```text
BLOCKER

mysql-data 使用 Docker Named Volume。

MVP 暂不支持自动迁移 Docker Volume 数据。

请先完成数据迁移，或将该服务标记为外部数据源。
```

第二阶段再开发 Docker Volume Mover。

# 5. 第一阶段开发顺序

严格按照以下顺序：

## Phase 1：UI Shell

完成：

- React 项目结构
- Router
- Sidebar
- Header
- Design Token
- Mock Data
- Overview
- Migration List

不得连接 Velero。

## Phase 2：Environment

完成：

- Environment CRUD
- Kubernetes Connection
- SmartX SKS Connection
- Credential Storage
- Connection Test

## Phase 3：Inventory + Assessment

完成：

- Kubernetes Namespace Discovery
- Resource Inventory
- Compose Parse
- Assessment Rule Framework
- BLOCKER / WARNING / INFO
- Assessment UI

## Phase 4：Compose Migration

完成：

- Kompose Adapter
- Temp Worker
- Manifest Parser
- Transform Engine
- Preview Diff
- Deploy SKS

## Phase 5：MigrationPlan + Preflight

完成：

- MappingProfile
- MigrationPlan
- Storage Mapping
- Namespace Mapping
- Ingress Mapping
- Registry Mapping
- Preflight

## Phase 6：Velero

完成：

- Velero Adapter
- Backup
- Restore
- BackupStorageLocation
- File System Backup
- Resource Status Watch

## Phase 7：MigrationRun

完成：

- MigrationRun
- MigrationStep
- SSE
- Timeline
- Volume Progress
- Failure Handling

## Phase 8：Validation

完成：

- Workload Validation
- Storage Validation
- Network Validation
- HTTP GET
- TCP Check
- Migration Report

# 6. Docker Compose MVP 验收标准

完整链路：

```text
compose.yaml
     ↓
成功解析
     ↓
完成 Assessment
     ↓
生成 Kubernetes Manifest
     ↓
完成 Mapping
     ↓
部署 SKS
     ↓
Pod Ready
     ↓
Application Check Passed
```

必须至少支持一个包含：

- nginx
- application service
- redis

的 Compose 示例。

# 7. Kubernetes MVP 验收标准

完整链路：

```text
Source K8s
     ↓
选择 Namespace
     ↓
Assessment
     ↓
Storage Mapping
     ↓
Velero Backup
     ↓
PVC Backup
     ↓
Restore SKS
     ↓
Pod Ready
     ↓
PVC Bound
     ↓
Application Check Passed
```

成功条件：

- Kubernetes 资源完整
- Pod Ready
- PVC Bound
- Service Endpoint 正常
- Health Check Passed

# 8. Demo A：Docker Compose → SKS

演示故事线：

```text
上传 docker-compose.yml

        ↓

发现：

nginx
java-api
redis

        ↓

Assessment：

Redis 有 Volume
nginx 端口需要 Service
API 无 Resource Request

        ↓

生成 Kubernetes Manifest

        ↓

SKS Transform

        ↓

映射：

Storage → zbs-sc
Ingress → contour

        ↓

Preflight

        ↓

Deploy

        ↓

3 / 3 Pods Ready

        ↓

HTTP Health Check Passed

        ↓

Migration Successful
```

重点展示：

- 自动发现
- 风险评估
- 自动转换
- SmartX SKS Target Mapping
- 验证结果

# 9. Demo B：Kubernetes → SKS

演示故事线：

```text
连接 Source Kubernetes

        ↓

选择 namespace: order

        ↓

发现：

8 Workloads
3 PVC
5 Services
1 Ingress

        ↓

Assessment

        ↓

Storage Mapping：

vsphere-sc → zbs-sc

        ↓

Ingress Mapping：

nginx → contour

        ↓

Velero Backup

        ↓

PVC Data Transfer

        ↓

Restore SKS

        ↓

13 / 13 Pods Ready

3 / 3 PVC Bound

        ↓

HTTP Check Passed

        ↓

Migration Successful
```

重点展示：

- Namespace Discovery
- StorageClass 差异
- Velero Backup / Restore
- PVC Data Transfer
- 迁移 Timeline
- Migration Report

# 10. MVP 完成定义

只有以下两条链路均能完整演示时，MVP 才视为完成：

```text
Docker Compose → Assessment → Transform → Deploy → Validate
```

以及：

```text
Kubernetes → Assessment → Mapping → Velero → Restore → Validate
```

# 11. Codex 工作原则

Codex 开发时必须遵守：

1. 不得擅自扩展 MVP 范围
2. 不得以 Velero CLI 作为正式后端架构
3. Kompose 输出不得直接 Apply
4. 所有迁移前必须执行 Assessment
5. 所有迁移前必须执行 Preflight
6. 所有迁移完成必须执行 Validation
7. 所有状态由 Backend 状态机统一维护
8. UI 不感知 Velero CRD 细节
9. StorageClass 等目标配置不得硬编码
10. 优先保证端到端可运行，而不是增加页面数量

# 12. 推荐每个 Phase 的交付方式

每个 Phase 完成后 Codex 必须输出：

- 已完成能力
- 修改文件列表
- API 变更
- 数据库变更
- 未完成项
- 已知问题
- 本地验证方式

禁止直接进入下一 Phase 而不验证当前 Phase。
