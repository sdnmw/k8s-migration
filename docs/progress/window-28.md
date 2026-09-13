# Window 28 — Compose 变量化项目发现修复

## 结果

修复 Docker Compose 自动发现会静默遗漏大量使用 `${VAR}` 的项目。`openclaw` 的发现配置由 `docker compose config --no-interpolate` 产生；旧解析器在端口、用户、镜像和卷源等强类型字段中遇到未展开变量时拒绝配置，应用服务随后跳过该项目，所以界面和迁移向导只剩 `sks-nacos-demo`。

新解析器在自动发现且未提供 `.env` 时，仅为 Compose 语法归一化生成确定性的无敏感占位值，解析完成后恢复 Inventory 中的原始变量引用。实际环境变量值仍不会从源主机读取或写入平台。

## 本窗口完成

- 自动发现支持变量化的镜像、端口、用户和长格式 Volume source。
- Inventory 保留 `${OPENCLAW_IMAGE}`、`${OPENCLAW_DATA_DIR}` 等原始引用，不把占位值伪装成迁移配置。
- 检测到未展开变量时写入 `COMPOSE_UNRESOLVED_VARIABLES` 警告。
- 默认评估引擎把该警告转成可下钻的 BLOCKER，显示受影响 ComposeProject、原因和“上传受控 `.env` 后重新发现”的处理建议。
- 加入简化变量配置单测及可选真实发现配置集成测试。

## 实机证据

- `192.168.112.51` 的 Compose 列表包含 `openclaw`（running）和 `sks-nacos-demo`（exited）。
- 对 `/root/openclaw/docker-compose.yml` 执行只读 `docker compose ... config --no-interpolate` 后，新解析器成功识别 `openclaw` 及其服务，并产生未解析变量警告。
- 全量 AMD64 离线包校验完成并通过一键部署导入 Harbor。
- `mw` 平台升级到 Helm revision 10；API 使用新 digest，API、Worker、Web 与 PostgreSQL 全部 Ready。

## 测试门禁

- Go 全量测试、Go Vet、gofmt 门禁通过。
- OpenAPI lint 通过。
- 前端 13 个交互测试、ESLint、TypeScript 与生产构建通过。
- 真实 `openclaw` 无插值配置解析测试通过。

## 最终离线包

`output/sks-migration-center-0.1.0-amd64-openclaw-final2/sks-migration-center-0.1.0-linux-amd64.tar.gz`

大小为 608 MiB，SHA-256 为 `688731af8cddc71bfe1aa15046bbc897b659ec8704f03f60216ef318bb67276a`，解包后包含 284 个文件。

平台地址仍为 `http://192.168.118.206:31500`。登录后在“源环境”对 `compose-112-51` 再点一次“发现应用”，即可重新登记 `openclaw`；其未解析变量会在迁移评估中明确阻止继续，直至使用手工注册入口上传对应受控 `.env`。
