# 窗口 02：API 契约、领域模型与 PostgreSQL

- 状态：完成
- 日期：2026-09-02
- 门禁：Schema/API 契约测试通过

## 已完成能力

- 建立 OpenAPI 3.1 契约，覆盖认证、环境、存储、对象存储、Add-on、Assessment、迁移、SSE、人工切流与回滚入口。
- 建立 Environment、StorageProfile、SourceApplication、Assessment、MappingProfile、MigrationPlan/Run、Validation、ObjectStorage 与 Addon 领域模型。
- 建立首版 PostgreSQL Schema，共 21 张业务与迁移管理表。
- 数据库迁移使用嵌入式 SQL、PostgreSQL advisory lock 和 SHA-256 checksum，重复执行幂等，已执行迁移禁止静默修改。
- API 启动时连接 PostgreSQL并自动迁移；`/readyz` 检查数据库可用性，失败返回 RFC 9457 Problem。
- 建立 Environment Repository 接口与 pgx 实现，支持 CRUD、JSONB 能力快照、NotFound/Conflict 错误映射。

## API 变更

- API 契约文件：`api/openapi.yaml`。
- 公开接口前缀固定为 `/api/v1`。
- 错误响应使用 `application/problem+json`，包含稳定 `code` 字段。
- SSE 契约支持 `Last-Event-ID`，具体事件持久化和推送从窗口 15 实现。

## 数据库变更

首版迁移 `000001_initial.sql` 创建：

- 管理员、会话、加密凭证。
- 环境、存储 Profile、对象存储 Profile、Add-on 安装状态。
- 源应用、Assessment 与问题项、Mapping Profile。
- MigrationPlan、MigrationRun、MigrationStep、VolumeTransfer。
- ValidationResult、CutoverConfirmation、MigrationEvent、AuditEvent、JobLease。

Schema 通过约束保证枚举值、状态范围、源/目标不同、目标必须为 Kubernetes、字节进度和唯一性规则。

## 验证结果

- Redocly OpenAPI 校验：有效，无错误。
- OpenAPI 必要路径契约测试：通过。
- Go 单元测试、`go vet`、格式检查：通过。
- PostgreSQL 18 真实容器迁移：通过，创建 21 张 public 表。
- 同一迁移重复执行：通过。
- Environment Repository 真实 PostgreSQL Create/Get/List/Update/Delete：通过。
- API 数据库就绪与 503 Problem 分支测试：通过。
- `make lint && make test && make build`：通过。

## 未完成项

- JobLease 的领取、心跳、过期接管与状态机事务将在窗口 03 实现。
- 管理员初始化、会话和凭证加密将在窗口 04 实现。
- OpenAPI 当前只定义部分请求/响应细节；各业务窗口实现时继续收紧 Schema。

## 已知问题

- OpenAPI 推荐规则仍报告部分操作缺少显式 4xx 响应警告；契约有效，业务实现时逐接口补齐。
- Docker Hub 在当前网络超时，真实 PostgreSQL 测试使用本机已有的官方 PostgreSQL 18 Alpine 镜像；正式离线镜像来源将在窗口 16 固化。

## 本地验证

```bash
make lint
make test
make build

TEST_DATABASE_URL='postgres://migration:migration@127.0.0.1:5432/migration?sslmode=disable' \
  go test -v ./internal/repository/postgres
```

