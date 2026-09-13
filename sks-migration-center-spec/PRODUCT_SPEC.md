# PRODUCT_SPEC.md

# 1. 产品定位

产品名称：**SKS Migration Center**

产品目标：为 SmartX SKS 提供统一的 Docker Compose 与 Kubernetes 应用迁移工作台。

系统面向两类源端：

## 1.1 Docker Compose → SmartX SKS

典型来源：

- 单机 Docker
- Docker Compose
- VM 内运行的 Docker Compose
- 开发测试环境 Compose

迁移目标：

```text
Docker Compose
      │
      ├── Workload
      ├── Network
      ├── Volume
      ├── Config
      └── Image
      │
      ▼
SmartX SKS
      │
      ├── Deployment / StatefulSet
      ├── Service / Ingress
      ├── PVC
      ├── ConfigMap / Secret
      └── Registry
```

## 1.2 Kubernetes → SmartX SKS

典型来源：

- 原生 Kubernetes
- Rancher / RKE2
- VMware Tanzu
- OpenShift
- 老版本 Kubernetes
- 其他厂商 Kubernetes

目标：

```text
Source Kubernetes
        │
        ├── Kubernetes Resources
        ├── PVC Data
        ├── Image
        └── Configuration
        │
        ▼
SmartX SKS
```

# 2. 产品设计原则

系统不得设计成：

```text
Web UI
 │
 ├─ kompose convert
 └─ velero restore
```

而应设计成：

```text
               SKS Migration Center
                        │
                Migration Workflow
                        │
       ┌────────────────┼─────────────────┐
       │                │                 │
 Assessment Engine  Transform Engine  Migration Engine
       │                │                 │
       │                │          ┌──────┴──────┐
       │                │          │             │
       ▼                ▼        Velero       Kompose
 Source Analysis    SKS Profile
                        │
                        ▼
                  SmartX SKS
```

Velero 和 Kompose 都只是底层执行引擎。

产品自身核心能力：

1. Source Discovery
2. Migration Assessment
3. Target Mapping
4. Manifest Transformation
5. Preflight
6. Migration Orchestration
7. Validation
8. Migration Report

# 3. 用户角色

MVP 仅设计一个角色：

## Administrator

主要用户：

- SmartX SE
- SmartX PS
- 客户容器管理员
- 客户基础设施管理员

MVP 暂不实现：

- 多租户
- 项目级权限
- LDAP
- SSO
- 完整 RBAC

# 4. 统一迁移流程

无论源端为 Compose 还是 Kubernetes，产品统一使用：

```text
发现 → 评估 → 映射 → 检查 → 迁移 → 验证
```

不要向用户暴露两套完全不同的操作范式。

# 5. Docker Compose 迁移流程

```text
创建迁移任务
     │
     ▼
选择 Docker Compose
     │
     ▼
上传 compose.yaml / .env
     │
     ▼
解析 Compose
     │
     ▼
应用发现
     │
     ▼
迁移评估
     │
     ▼
生成 Candidate Kubernetes Manifest
     │
     ▼
SKS Transform
     │
     ▼
目标映射
     │
     ▼
Preflight
     │
     ▼
用户确认
     │
     ▼
部署 SKS
     │
     ▼
Validation
     │
     ▼
Migration Report
```

Kompose 只负责：

```text
Compose
   ↓
Candidate Kubernetes Manifest
```

Kompose 输出禁止直接 Apply 到目标集群。

# 6. Kubernetes 迁移流程

```text
创建迁移任务
     │
     ▼
选择 Source Kubernetes
     │
     ▼
连接源集群
     │
     ▼
选择 Namespace
     │
     ▼
Inventory
     │
     ▼
Assessment
     │
     ▼
Resource Mapping
     │
     ▼
Preflight
     │
     ▼
Velero Backup
     │
     ├── Kubernetes Resource
     │
     └── PVC Data
     │
     ▼
Transform
     │
     ▼
Velero Restore
     │
     ▼
Validation
     │
     ▼
Migration Report
```

MVP 默认以 Namespace 作为 Kubernetes 迁移最小单元。

# 7. Assessment Engine

Assessment 是整个产品的核心能力之一。

统一风险级别：

## BLOCKER

必须处理，否则禁止迁移。

例如：

- 目标集群不存在对应 StorageClass
- 目标节点架构不兼容镜像
- CRD 缺失
- hostPath Volume
- PVC AccessMode 不支持

## WARNING

允许继续，但要求用户确认。

例如：

- 无 Resource Request
- privileged
- 无 readinessProbe
- IngressClass 需要转换
- NodeSelector 在目标端不存在

## INFO

优化建议。

例如：

- 建议增加副本
- 建议配置 PDB
- 建议增加 Resource Limit

Assessment 必须检查：

### Kubernetes Compatibility

- Kubernetes Version
- API Version
- CRD
- Admission Requirement

### Scheduling

- CPU
- Memory
- GPU
- NodeSelector
- NodeAffinity
- Toleration
- Architecture

### Storage

- StorageClass
- AccessMode
- VolumeMode
- CSI
- Snapshot
- hostPath
- local PV
- PVC Capacity

### Network

- ServiceType
- IngressClass
- hostNetwork
- hostPort
- NetworkPolicy
- Multus

### Security

- privileged
- hostPID
- hostIPC
- hostNetwork
- runAsRoot
- Linux Capabilities

### Image

- Registry availability
- Authentication
- amd64 / arm64
- Image existence

### Dependency

- ConfigMap
- Secret
- ServiceAccount
- Role
- RoleBinding
- CRD
- PVC

# 8. Target Mapping

系统必须支持以下映射：

## Storage Mapping

```text
vsphere-default → zbs-sc
ceph-rbd        → zbs-sc
nfs-client      → sks-nfs
```

## Namespace Mapping

```text
production → production
order      → order
```

## Ingress Mapping

```text
nginx → contour
```

## Registry Mapping

```text
registry.old.local/order
        ↓
harbor.sks.local/order
```

## Node Label Mapping

当目标端不存在源端 Node Label 时，允许用户：

- 删除
- 修改
- 保留并接受风险

# 9. SKS Target Profile

系统必须存在可配置的 Target Profile：

```yaml
name: smartx-sks

storage:
  defaultStorageClass: zbs-sc

ingress:
  defaultIngressClass: contour

service:
  defaultType: ClusterIP

security:
  allowPrivileged: false

transform:
  removeNodeName: true
  removeClusterIP: true
  rewriteStorageClass: true
  rewriteIngressClass: true
  rewriteRegistry: true
```

实际 StorageClass、IngressClass、Registry 等不得硬编码。

# 10. Transform Engine

Transform Engine 必须作为独立模块存在。

至少支持：

- StorageClass Rewrite
- IngressClass Rewrite
- Namespace Rewrite
- Registry Rewrite
- Remove ClusterIP
- Remove NodeName
- NodeSelector Rewrite
- Remove Runtime Metadata

必须支持 Preview Diff。

例如：

```diff
- storageClassName: vsphere-default
+ storageClassName: zbs-sc
```

# 11. Preflight

迁移开始前必须检查：

- 目标集群连接
- Namespace
- StorageClass
- PVC Capacity
- CPU
- Memory
- Registry
- Ingress
- CRD
- NodeSelector
- Architecture
- Security constraints

存在 BLOCKER 时，“开始迁移”按钮必须禁用。

# 12. Validation

迁移成功不能只依据 kubectl apply 或 Velero Restore 成功。

必须至少验证：

## Workload

- Deployment Available
- StatefulSet Ready
- Pod Ready

## Storage

- PVC Bound
- Volume Mounted

## Network

- Service Endpoint Exists
- Ingress Endpoint Exists

## Application

MVP 支持：

- HTTP GET
- TCP Connection

# 13. 产品明确不做什么

MVP 不实现：

- Kubernetes Dashboard
- 集群生命周期管理
- Helm 管理
- VM 迁移
- Forklift
- KubeVirt
- 数据库复制
- 不停机迁移
- 自动 DNS Cutover
- 自动 LoadBalancer Cutover
- 完整 Rollback
- CI/CD
- GitOps
- Argo CD
- Service Mesh
