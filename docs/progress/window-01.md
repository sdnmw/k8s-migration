# 窗口 01：工程基线

- 状态：完成
- 日期：2026-09-02
- 门禁：空项目可一键构建、测试、启动

## 已完成能力

- 建立 Go API Server 与独立 Worker 入口。
- 提供 `/healthz`、`/readyz`、`/api/v1/system/info`。
- 建立 React、TypeScript、Ant Design、TanStack Query 与 Router 前端入口。
- 建立 PostgreSQL、API、Worker、Web 的 Docker Compose 本地拓扑。
- 建立后端、前端和开发容器镜像定义。
- 建立 GitHub Actions 后端与前端 CI。
- 固定 Go 1.26.5、Node.js 24.20.0 构建基线。
- 记录系统架构及离线交付 ADR。

## API 变更

| Method | Path | 用途 |
|---|---|---|
| GET | `/healthz` | 进程存活检查 |
| GET | `/readyz` | 服务就绪检查 |
| GET | `/api/v1/system/info` | 产品版本与运行环境 |

## 数据库变更

本窗口仅建立 PostgreSQL 本地服务，尚未创建业务表。数据库 Schema 与迁移从窗口 02 开始。

## 验证结果

- Go 单元测试：通过。
- Go API/Worker 编译：通过。
- API 启动、健康检查、就绪检查、优雅停止：通过。
- 前端 ESLint：通过。
- 前端 Vitest：1 个测试通过。
- 前端 TypeScript 与 Vite 生产构建：通过。
- `docker compose config --quiet`：通过。
- npm audit：0 个已知漏洞。

## 未完成项

- OpenAPI 与数据库 Schema。
- PostgreSQL 连接和 Repository。
- 管理员登录、凭证加密、迁移领域逻辑。
- 完整导航和业务页面。
- Kubernetes、Velero、MinIO、NFS 与 Compose 适配器。

## 已知问题

- 当前前端为单入口包，Vite 提示主包超过 500 kB；将在 UI 路由窗口通过页面懒加载拆包。
- 宿主机没有 Go，验证时使用经官方 SHA-256 校验的临时 Go 1.26.5 工具链；正式开发命令默认使用固定 Go 容器。
- 尚未执行完整 Docker 镜像构建；镜像仓库响应较慢，但 Docker Compose 配置已验证。

## 本地验证

```bash
make bootstrap
make lint
make test
make build
make dev
```

