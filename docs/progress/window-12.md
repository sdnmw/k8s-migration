# 窗口 12：MappingProfile 与六类目标映射

- 状态：完成
- 日期：2026-09-03
- 门禁：映射 CRUD 和冲突校验通过

## 已完成能力

- MappingProfile 支持 StorageClass、外部 NFS、Namespace、IngressClass、Registry 和 NodeLabel 六类映射。
- 完整实现 PostgreSQL Repository、应用服务、REST CRUD、审计事件和稳定错误码。
- 列表 API 支持按 `targetEnvironmentId` 过滤；所有返回数组为空时保持 `[]`，避免前端出现 null 分支。
- 新增独立数据库迁移，为既有 MappingProfile 无损增加 `nfs_mappings` JSONB 字段。
- 创建和更新均校验目标必须为 `TARGET/KUBERNETES` 环境，并使用该目标最新能力快照验证 SC 与 IngressClass。

## 冲突与格式门禁

- 每类映射的 source 必须唯一。
- Namespace 必须符合 DNS label，并拒绝多个源 Namespace 合并到同一目标 Namespace。
- Registry 拒绝重叠的源前缀和相同的目标前缀，避免镜像改写顺序不确定或目标覆盖。
- StorageClass 和 IngressClass 目标必须存在于目标能力快照；快照尚无该类别时允许先保存，Preflight 再做在线确认。
- NFS source/target server、export、目标 SC 均必填，export 必须是绝对路径；拒绝重复源与多个源写入同一目标 export。
- NodeLabel 仅支持 MAP/DROP；名称必须符合 Kubernetes qualified name；MAP 目标必填且不能发生目标碰撞，DROP 会清除多余 target。
- 映射项在持久化前按 source 确定性排序，确保 Diff 稳定。

## API

- `GET /api/v1/mapping-profiles[?targetEnvironmentId=...]`
- `POST /api/v1/mapping-profiles`
- `GET /api/v1/mapping-profiles/{profileId}`
- `PUT /api/v1/mapping-profiles/{profileId}`
- `DELETE /api/v1/mapping-profiles/{profileId}`
- OpenAPI 定义六类映射的输入/输出 Schema 及 400/404/409 契约；未实现接口告警降至 18 条。

## 管理界面

- “存储映射”和“镜像仓库”导航统一进入目标映射管理页，并按入口打开对应默认页签。
- 列表显示目标 SKS、六类映射计数、更新时间及编辑/删除操作。
- 1000px 编辑器提供六个页签；StorageClass、IngressClass 与 NFS 目标 SC 选项来自所选目标集群能力快照。
- NFS 映射完整展示源/目标 server、export 与目标 StorageClass；NodeLabel 支持 MAP/DROP 动态表单。
- 后端错误会以明确错误信息留在编辑器中，不会关闭或丢失用户输入。

## 验证

- 领域测试覆盖归一化、排序、Namespace/Registry/NodeLabel/NFS 碰撞、未知目标 SC/Ingress 与非法路径/动作。
- 服务测试覆盖目标角色门禁、时间戳/ID 生成和空数组归一化。
- API 测试覆盖六类输入解码、目标过滤和 409 冲突映射。
- PostgreSQL 18 实例完成 schema migration、六类 JSONB 写入、读取、过滤列表、更新和删除集成测试。
- 1280×720 浏览器实测列表、编辑器、六个页签、NFS 五字段行与 NodeLabel 动态表单；页面和编辑器无水平溢出，控制台无告警。
- `go test -race`、`make test`、`make lint`、`make build`：通过。

## 已知边界

- Registry 本窗口只管理映射规则；凭证、远程 manifest、digest 与 Harbor 复制执行会在后续镜像迁移窗口接入。
- 外部 NFS 映射不代表安装完成；窗口 18 会安装离线 NFS CSI、创建 StorageClass 并执行集群内读写探测。
- 映射实际作用于 Manifest 的能力由窗口 13 Transform Engine 实现。
