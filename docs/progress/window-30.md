# Window 30 — SKS 交互与详情展示统一

## 参考页面与观察

2026-09-09 通过用户已打开的 Chrome 页面检查 sida / iwei-order / order-web Deployment。查看 Pod 详情、访问方式页签，并打开编辑 Deployment 面板后关闭，未提交业务修改。

参考特征：紧凑的标题与返回入口；双列属性；浅蓝页签；关联资源分层展示；编辑表单独立滚动；标题和底部操作位置稳定；轻边框、低阴影、简短过渡。

## 实现

- 统一 Ant Design 主题：32px 控件、按钮层级、表格表头、页签、表单标签和间距。
- 公共 PageHeader 提供详情返回入口、长标题换行、可折行操作栏。
- 编辑表单使用 EditorModal：统一标题、独立滚动内容、固定底部按钮。应用于环境、Compose 注册、资源/镜像映射、存储、S3/MinIO、凭证页面。
- 能力快照、Namespace Inventory、兼容性评估采用 DetailDrawer：统一关闭入口、底部关闭按钮、展开/还原宽度。
- 任务详情页签保存到 URL 的 tab 参数，支持刷新恢复和浏览器返回。
- 拓扑节点详情改为双列概要以及资源属性、转换差异、关联资源页签；属性不再限制为前八项。
- 全站加入 120–240ms 的控件及页面过渡、键盘 focus-visible、高对比选中态，并响应 prefers-reduced-motion。
- 管理员入口增加下拉指示；迁移向导操作区保持底部可见。

## 验证

- TypeScript/Vite 生产构建通过，ESLint 通过，14 个前端交互测试通过。
- 浏览器检查环境列表、添加环境表单、任务信息详情。
- 1280px 页面 DOM 检查：innerWidth 和 scrollWidth 均为 1280。
- 点击数据迁移后 URL 变为 tab=data；刷新后该页签 aria-selected=true。
- 能力抽屉打开、展开入口切换、底部关闭后消失、再次打开通过。
- 视觉检查使用开发预览数据；线上升级另以实际镜像、静态资源与 Pod Ready 核验。

## 离线交付

包：output/sks-migration-center-0.1.0-amd64-sks-ui-final/sks-migration-center-0.1.0-linux-amd64.tar.gz

大小约 608 MiB，16 个 linux/amd64 镜像。SHA-256：c0006e2728d161c52a6933d5e3ac8dc414a6a03d38b683d997ab40531e7260cd。

Web digest：sha256:72eff87236a63cfcd3b671642152084e32434597ac4271380963d76795aafa19。

mw 一键部署完成，Helm revision 12，Web rollout 成功。
