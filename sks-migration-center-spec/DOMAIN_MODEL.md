# DOMAIN_MODEL.md

# 1. 技术架构

推荐：

```text
Frontend
React
TypeScript
Ant Design
TanStack Query

          │ REST / SSE

Migration API Server
Go

          │
   ┌──────┼───────────────┐
   │      │               │
Postgres Worker        Kubernetes
          │
          ▼
Migration Engine
   │
   ├─ Compose Adapter
   ├─ Kubernetes Adapter
   ├─ Kompose Adapter
   ├─ Velero Adapter
   ├─ Transform Engine
   ├─ Assessment Engine
   └─ Validation Engine
```

实时进度使用 SSE：

```text
GET /api/v1/migrations/:id/events
```

MVP 不引入 WebSocket。

# 2. 数据库

使用 PostgreSQL。

MVP 不引入：

- Redis
- Kafka
- RabbitMQ

# 3. Environment

```typescript
Environment {
  id: UUID

  name: string

  type:
    DOCKER_COMPOSE |
    KUBERNETES |
    SMARTX_SKS

  endpoint?: string

  credentialRef?: UUID

  kubernetesVersion?: string

  architecture?: string[]

  status:
    CONNECTED |
    DISCONNECTED |
    ERROR

  metadata: JSON

  createdAt: timestamp
  updatedAt: timestamp
}
```

# 4. Credential

```typescript
Credential {
  id: UUID

  name: string

  type:
    KUBECONFIG |
    SSH |
    REGISTRY |
    S3

  encryptedPayload: bytes

  createdAt: timestamp
  updatedAt: timestamp
}
```

API 不得返回：

- encryptedPayload
- password
- privateKey
- token

Secret 只允许：

- create
- replace
- delete

不允许 Read Back。

# 5. SourceApplication

```typescript
SourceApplication {
  id: UUID

  environmentId: UUID

  name: string

  sourceType:
    COMPOSE |
    KUBERNETES

  namespace?: string

  inventory: ResourceInventory

  createdAt: timestamp
}
```

# 6. ResourceInventory

```typescript
ResourceInventory {
  workloads: ResourceSummary[]
  services: ResourceSummary[]
  ingresses: ResourceSummary[]
  configMaps: ResourceSummary[]
  secrets: ResourceSummary[]
  pvc: VolumeSummary[]
  crds: ResourceSummary[]
  images: ImageSummary[]
}
```

# 7. Assessment

```typescript
Assessment {
  id: UUID

  applicationId: UUID

  score: number

  blockerCount: number
  warningCount: number
  infoCount: number

  issues: AssessmentIssue[]

  status:
    PENDING |
    RUNNING |
    COMPLETED |
    FAILED

  createdAt: timestamp
}
```

# 8. AssessmentIssue

```typescript
AssessmentIssue {
  id: UUID

  severity:
    BLOCKER |
    WARNING |
    INFO

  category:
    COMPUTE |
    STORAGE |
    NETWORK |
    SECURITY |
    IMAGE |
    API |
    DEPENDENCY

  resourceKind: string
  resourceNamespace?: string
  resourceName: string

  ruleId: string

  title: string
  description: string
  remediation?: string

  autoFixable: boolean
}
```

Rule ID 示例：

```text
STORAGE_HOSTPATH_001
NETWORK_INGRESS_CLASS_001
IMAGE_ARCH_001
API_DEPRECATED_001
```

检查逻辑不得和前端文案绑定。

# 9. MappingProfile

```typescript
MappingProfile {
  id: UUID

  name: string

  targetEnvironmentId: UUID

  storageMappings: KeyValueMapping[]
  namespaceMappings: KeyValueMapping[]
  ingressMappings: KeyValueMapping[]
  registryMappings: KeyValueMapping[]
  nodeLabelMappings: KeyValueMapping[]

  createdAt: timestamp
}
```

# 10. MigrationPlan

MigrationPlan 是系统核心对象。

```typescript
MigrationPlan {
  id: UUID

  name: string

  sourceType:
    COMPOSE |
    KUBERNETES

  sourceEnvironmentId?: UUID
  targetEnvironmentId: UUID

  sourceApplicationId: UUID
  assessmentId: UUID
  mappingProfileId: UUID

  migrationStrategy: MigrationStrategy
  validationPolicy: ValidationPolicy

  status:
    DRAFT |
    READY |
    BLOCKED |
    RUNNING |
    COMPLETED |
    FAILED

  createdAt: timestamp
  updatedAt: timestamp
}
```

# 11. MigrationStrategy

```typescript
MigrationStrategy {
  resourceMode:
    KOMPOSE |
    VELERO

  volumeMode:
    NONE |
    FILE_SYSTEM_BACKUP |
    CSI_DATA_MOVER

  overwriteExistingResources: boolean

  preserveNodePort: boolean
}
```

# 12. MigrationRun

Plan 可以执行多次。

```text
MigrationPlan != MigrationRun
```

```typescript
MigrationRun {
  id: UUID

  migrationPlanId: UUID

  runNumber: number

  status:
    PENDING |
    PREFLIGHT |
    BACKUP |
    TRANSFER |
    TRANSFORM |
    RESTORE |
    VALIDATION |
    COMPLETED |
    FAILED |
    CANCELLED

  progress: number

  bytesTotal?: number
  bytesTransferred?: number

  startedAt?: timestamp
  completedAt?: timestamp

  errorCode?: string
  errorMessage?: string
}
```

# 13. MigrationStep

```typescript
MigrationStep {
  id: UUID

  migrationRunId: UUID

  type:
    PREFLIGHT |
    INVENTORY |
    BACKUP |
    VOLUME_TRANSFER |
    TRANSFORM |
    RESTORE |
    VALIDATION

  status:
    PENDING |
    RUNNING |
    SUCCEEDED |
    FAILED |
    SKIPPED

  progress: number

  startedAt?: timestamp
  completedAt?: timestamp

  summary?: string
}
```

# 14. VolumeTransfer

```typescript
VolumeTransfer {
  id: UUID

  migrationRunId: UUID

  sourcePVC: string
  targetPVC: string
  namespace: string

  totalBytes: number
  transferredBytes: number
  throughputBytesPerSecond: number

  status:
    PENDING |
    RUNNING |
    COMPLETED |
    FAILED
}
```

# 15. ValidationResult

```typescript
ValidationResult {
  id: UUID

  migrationRunId: UUID

  category:
    WORKLOAD |
    STORAGE |
    NETWORK |
    APPLICATION

  name: string

  status:
    PASSED |
    FAILED |
    WARNING

  message: string
}
```

# 16. AuditEvent

```typescript
AuditEvent {
  id: UUID

  actor: string
  action: string
  objectType: string
  objectId: UUID

  result:
    SUCCESS |
    FAILURE

  detail: JSON

  createdAt: timestamp
}
```

必须审计：

- 添加集群
- 删除集群
- 创建凭证
- 替换凭证
- 创建迁移
- 开始迁移
- 取消迁移
- 修改 Mapping

# 17. 状态机

```text
DRAFT
  │
  ▼
ASSESSING
  │
  ├──── failure ───→ FAILED
  │
  ▼
MAPPING
  │
  ▼
PREFLIGHT
  │
  ├──── blocker ───→ BLOCKED
  │
  ▼
READY
  │
  ▼
RUNNING
  │
  ├── BACKUP
  │
  ├── TRANSFER
  │
  ├── TRANSFORM
  │
  ├── RESTORE
  │
  └── VALIDATION
  │
  ├──── failure ───→ FAILED
  │
  ▼
COMPLETED
```

前端不得自行推断状态。

# 18. REST API

统一前缀：

```text
/api/v1
```

核心接口：

```text
GET    /environments
POST   /environments
GET    /environments/:id
DELETE /environments/:id

POST   /environments/:id/test

POST   /compose/analyze

POST   /applications/discover

POST   /assessments
GET    /assessments/:id

GET    /mapping-profiles
POST   /mapping-profiles

POST   /migration-plans
GET    /migration-plans/:id
PUT    /migration-plans/:id

POST   /migration-plans/:id/preflight

POST   /migration-plans/:id/runs

GET    /migration-runs/:id
POST   /migration-runs/:id/cancel

GET    /migration-runs/:id/events

GET    /migration-runs/:id/report
```

# 19. Velero Adapter 约束

禁止将以下方式作为主要实现：

```go
exec.Command("velero", ...)
```

Backend 应通过 client-go 创建并 Watch Velero CR：

- Backup
- Restore
- BackupStorageLocation
- DataUpload
- DataDownload
- PodVolumeBackup
- PodVolumeRestore

Velero CLI 仅限 Debug / Development。

# 20. Kompose Adapter 约束

Kompose 可以使用 CLI，但必须在隔离 Worker 中运行。

```text
API
 │
 ▼
Worker
 │
 ▼
Temp Workspace
 │
 ▼
kompose convert
```

Worker 必须：

- 独立临时目录
- CPU Limit
- Memory Limit
- Execution Timeout
- 禁止 HostPath
- 完成后删除 Workspace

输出流程：

```text
Kompose Output
       ↓
Manifest Parser
       ↓
Transform Engine
       ↓
Target Manifest
```

# 21. Transform Engine

独立 package：

```text
internal/engine/transform
```

至少支持：

- StorageClass Rewrite
- IngressClass Rewrite
- Namespace Rewrite
- Registry Rewrite
- Remove ClusterIP
- Remove NodeName
- NodeSelector Rewrite
- Remove Runtime Metadata

# 22. 工程目录

前端：

```text
src/

  api/

  components/
    assessment/
    mapping/
    migration/
    environment/
    common/

  pages/
    Overview/
    Migrations/
    MigrationCreate/
    MigrationDetail/
    Environments/
    MappingProfiles/
    Settings/

  hooks/
  stores/
  types/

  theme/
    tokens.ts
    global.css

  routes/
```

后端：

```text
cmd/
  server/

internal/

  api/

  domain/
    environment/
    application/
    assessment/
    migration/
    mapping/
    validation/

  engine/
    assessment/
    migration/
    transform/
    validation/

  adapter/
    kubernetes/
    velero/
    compose/
    kompose/
    registry/
    objectstorage/

  repository/
  credential/
  worker/
  event/

pkg/
```

禁止把所有代码写进 `handlers/`。

# 23. 分层约束

任何新功能必须先归类：

- Domain
- Engine
- Adapter
- UI

依赖方向：

```text
UI
 ↓
API
 ↓
Domain / Engine
 ↓
Adapter
```

禁止：

- Assessment Engine 操作 HTTP Response
- React 页面理解 Velero CRD
- Velero Adapter 决定 UI 风险等级
