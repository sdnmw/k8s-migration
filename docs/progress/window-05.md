# 窗口 05：SmartX 风格 UI Shell 与 Mock 页面

- 状态：完成
- 日期：2026-09-02
- 门禁：1440×900、1280px 布局验收通过

## 已完成能力

- 建立 216px 固定侧栏、56px 顶栏、24px 内容边距的企业基础设施管理 UI Shell。
- 按 `UI_SPEC.md` 建立概览、迁移任务、创建迁移、迁移详情、源环境、SmartX SKS、存储映射、镜像仓库、转换规则、对象存储和凭证路由。
- 导航仅围绕迁移业务，不加入 Pod、Deployment、Node、CRD、Helm 或 Kubernetes Dashboard 入口。
- 概览页提供 4 个克制的数字卡片、最近迁移任务表格与源/目标环境健康状态，不堆叠复杂图表。
- 迁移任务页提供状态分段筛选、关键词搜索和唯一主操作“创建迁移”。
- 建立六步迁移向导首屏，统一呈现 Kubernetes 与 Docker Compose 两种来源。
- 建立迁移详情纵向时间线、逐 PVC 传输表格、吞吐信息与人工切流提示。
- 建立真实管理员登录页，并接入窗口 4 的 `/auth/login`、`/auth/me`、`/auth/logout`、Cookie 与 CSRF 流程。
- 页面通过 React lazy route 拆包；入口 JS 从窗口 1 的约 594 kB 降至约 156 kB。
- 所有颜色、字号、间距、圆角、边框和语义状态均按 UI Spec Design Token 落地；未使用渐变、玻璃态、大圆角或大面积蓝色。

## 浏览器实测

### 1440 × 900

- Sidebar：216px。
- Header：56px，左边界为 216px。
- Content 左边界：216px，Padding：24px。
- 页面级水平溢出：无。
- 六步 Stepper 顺序完整，未选择来源时“下一步”禁用，选择 Kubernetes 后启用。
- 迁移详情包含 8 个时间线节点与逐卷传输表。

### 1280 × 900

- Sidebar：216px，Header：56px。
- 页面级水平溢出：无。
- 表格位于可用内容宽度内；数据量内容完整显示。
- 表格可见行高度为 49px（48px 内容行 + 1px 行分隔线）。

## 自动化验证

- ESLint：通过，0 warning。
- Vitest：登录页、认证 Shell/概览、向导门禁测试通过。
- TypeScript：严格模式构建通过。
- Vite 生产构建：通过，业务路由生成独立 chunk。

## SKS 页面校准说明

- 已按用户要求尝试接管现有 SKS 标签页，但当前内置浏览器没有返回可接管标签。
- 新开内置标签访问 `192.168.112.18` 超时；没有绕过 HTTPS 安全提示，也没有要求或读取登录凭证。
- 本窗口以工作区 `UI_SPEC.md` 为严格基线；真实 SKS 逐页像素和交互校准仍保留在窗口 26 的人工验收环节。

## 已知问题

- Ant Design 公共 vendor chunk 约 540 kB（gzip 约 175 kB），入口和业务页面已拆分；后续页面稳定后再按依赖边界细分 vendor chunk。
- 当前表格和环境数据为明确的 UI Mock，将在环境、Inventory 与迁移 API 窗口替换为真实查询。

## 本地验证

```bash
cd web
npm run lint
npm run test
npm run build
```
