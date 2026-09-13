# 窗口 04：管理员认证、凭证加密、脱敏与审计

- 状态：完成
- 日期：2026-09-02
- 门禁：凭证不可回读，安全测试通过

## 已完成能力

- 实现本地单管理员首次引导；数据库 singleton 约束保证不能创建第二个管理员。
- 密码使用 Argon2id，参数为 64 MiB 内存、3 次迭代、4 路并行、随机 16 字节 salt 和 32 字节摘要。
- 登录使用 256 位随机不透明 session token；数据库仅保存 SHA-256 摘要。
- Session Cookie 为 `HttpOnly`、`SameSite=Strict`，生产环境强制 `Secure=true`。
- CSRF 使用双提交 Cookie、`X-CSRF-Token` 请求头和数据库摘要三方恒定时间校验。
- 实现 `/api/v1/auth/login`、`/logout`、`/me` 与统一的受保护 API 前缀。
- 凭证使用 AES-256-GCM envelope encryption：每条记录生成独立 DEK，再由版本化主密钥包装。
- 凭证 ID 和类型作为 AEAD associated data，记录串换或密文篡改会解密失败。
- 主密钥和首次管理员密码通过只读 Secret 文件加载；不接受 API 回显或日志输出。
- 建立递归脱敏器，对 password、token、authorization、private key、kubeconfig、credential 等字段统一处理。
- 审计 Repository 在 JSON 入库前再次强制脱敏，已记录登录成功、失败和退出事件。
- 开发环境通过 `make dev` 在 Git 忽略目录生成随机密码和 256 位主密钥。

## API 行为

| 场景 | 结果 |
|---|---|
| 正确登录 | `204`，设置 Session/CSRF Cookie |
| 错误用户名或密码 | 统一 `401 AUTH_INVALID_CREDENTIALS` |
| 未登录访问受保护 API | `401 AUTH_REQUIRED` |
| 状态变更请求缺少或伪造 CSRF | `403 AUTH_CSRF_INVALID` |
| 退出后继续使用旧 Session | `401 AUTH_REQUIRED` |

`/api/v1/auth/me` 只返回管理员 ID 与用户名，不返回密码哈希、Session、CSRF 或任何凭证明文。

## 数据库变更

- 新增 `000002_security_hardening.sql`。
- `administrators.singleton` check + unique index 强制单管理员。
- 新增 Session token/expiry 联合索引。

## 验证结果

- Argon2id 正确/错误密码验证：通过。
- 随机化 envelope encryption 往返、AAD 绑定、密文篡改：通过。
- 递归日志/审计脱敏：通过。
- Cookie 属性、CSRF 缺失拒绝、退出失效：API 单元测试通过。
- 真实 API + PostgreSQL Smoke：登录 `204`、me `200`、无 CSRF 退出 `403`、正常退出 `204`、旧会话 `401`。
- PostgreSQL 真实集成：单管理员约束、密码/Session 摘要、凭证密文、审计脱敏全部通过。
- Go race detector：security、auth、credential、api、PostgreSQL Repository 全部通过。
- 测试后管理员、Session、Credential、MigrationPlan、JobLease 均清理为 0。

## 安全边界

- 开发环境允许 `COOKIE_SECURE=false` 以支持本机 HTTP；任何非 development 环境配置为 false 时进程拒绝启动。
- 正式部署必须由 Kubernetes Secret 提供主密钥与管理员引导密码，并由 Ingress 终止 TLS。
- 当前只加载一个活动主密钥版本；发布前的密钥轮换与历史 keyring 操作流程将在安全治理窗口补齐。
- 登录限速和 Session 定期清理将在 API 运维加固阶段补齐。

## 本地验证

```bash
make dev-secrets
make lint
make test
make build

TEST_DATABASE_URL='postgres://migration:migration@127.0.0.1:5432/migration?sslmode=disable' \
  go test -race -count=1 ./internal/security ./internal/auth ./internal/credential \
  ./internal/api ./internal/repository/postgres
```
