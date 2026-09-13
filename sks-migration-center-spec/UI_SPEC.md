# UI_SPEC.md

# 1. UI 定位

UI 风格：

> SmartX 企业基础设施管理产品风格，而不是 Kubernetes Dashboard 或消费级 SaaS。

设计关键词：

- 克制
- 专业
- 清晰
- 高信息密度
- 基础设施感
- 少装饰
- 强状态表达

# 2. 全局页面结构

左侧导航：

```text
概览

迁移任务

环境
 ├─ 源环境
 └─ SmartX SKS

迁移策略
 ├─ 资源映射
 ├─ 镜像仓库
 └─ 转换规则

系统设置
 ├─ 对象存储
 └─ 凭证
```

不要加入：

- Pods
- Deployments
- StatefulSets
- Nodes
- CRDs
- Helm
- Kubernetes Dashboard

# 3. Layout

目标桌面分辨率：

```text
1440 × 900
```

最低宽度：

```text
1280px
```

布局：

```text
┌──────────┬──────────────────────────────┐
│          │ Header 56                   │
│ Sidebar  ├──────────────────────────────┤
│ 216      │                              │
│          │ Content                      │
│          │                              │
└──────────┴──────────────────────────────┘
```

- Sidebar：216px
- Header：56px
- Content Padding：24px

# 4. 页面：首页概览

Route：

```text
/
```

顶部 4 个 Summary Card：

- 迁移任务
- 迁移成功
- 迁移中
- 需要处理

最近迁移任务表格字段：

- 名称
- 类型
- 源环境
- 目标
- 状态
- 进度
- 数据量
- 创建时间
- 操作

环境状态仅展示源环境和 SKS Target 的健康情况。

首页不得堆复杂图表。

# 5. 页面：迁移任务

Route：

```text
/migrations
```

字段：

- 任务名称
- 迁移类型
- 源环境
- 目标环境
- 应用 / Namespace
- 迁移状态
- 进度
- 数据量
- 创建时间
- 耗时
- 操作

筛选：

- 全部
- 进行中
- 成功
- 失败
- 需要处理

右上角唯一主操作：

```text
+ 创建迁移
```

# 6. 页面：创建迁移

Route：

```text
/migrations/new
```

首先选择：

```text
Docker Compose → SKS
Kubernetes → SKS
```

顶部 Stepper：

```text
1 源环境
2 选择应用
3 迁移评估
4 目标映射
5 迁移检查
6 确认
```

# 7. 页面：源环境

## Compose

支持：

- 上传 `compose.yaml`
- 上传 `.env`

解析后展示：

- Service 数量
- Volume 数量
- Network 数量

## Kubernetes

选择已有 Source Environment 或新增。

新增字段：

- 名称
- Kubeconfig

连接检查结果：

- API Server
- Authentication
- Kubernetes Version
- Namespace Count
- Node Count

不得显示完整 kubeconfig。

# 8. 页面：选择应用

## Compose

展示每个 Service：

- Image
- Ports
- Volume
- Depends On

## Kubernetes

左侧 Namespace，右侧资源摘要：

- Workloads
- PVC
- Service
- Ingress
- ConfigMap
- Secret

MVP 以 Namespace 作为最小迁移单元。

# 9. 页面：迁移评估

顶部：

```text
迁移就绪度 82 / 100

阻塞项 2
风险项 4
提示项 7
```

Assessment Table：

| 资源 | 类型 | 风险 | 问题 | 建议 |
|---|---|---|---|---|

支持筛选：

- 只看阻塞项
- 只看风险项
- 全部

# 10. 页面：目标映射

使用左右 Mapping UI。

支持：

- Storage Mapping
- Namespace Mapping
- Ingress Mapping
- Registry Mapping
- Node Label Mapping

映射项尽量使用 Dropdown，不要求用户编辑 YAML。

# 11. 页面：迁移检查

展示：

```text
目标集群连接       ✓
Namespace          ✓
StorageClass       ✓
PVC Capacity       ✓
CPU                ✓
Memory             ✓
Registry           ✓
Ingress            ✓
CRD                ✕
NodeSelector       ⚠
```

存在 BLOCKER：

```text
[开始迁移]
```

按钮 Disabled。

# 12. 页面：确认迁移

展示最终 Migration Plan：

- 任务名称
- Source
- Target
- Namespace
- Resources
- PVC
- Volume Data
- Storage Mapping
- Ingress Mapping

底部：

```text
[返回修改]
[开始迁移]
```

开始迁移需要二次确认。

# 13. 页面：迁移详情

Route：

```text
/migrations/:id
```

核心使用 Vertical Timeline：

```text
✓ 环境检查
✓ 创建备份
✓ Kubernetes 资源备份
● PVC 数据迁移
○ 资源转换
○ 目标恢复
○ 应用验证
○ 完成
```

PVC 必须逐卷展示：

- total bytes
- transferred bytes
- progress
- throughput

默认日志显示用户可理解事件。

高级入口：

```text
查看技术日志
```

# 14. 页面：迁移验证

展示：

## Workload

- Deployment Available
- StatefulSet Ready
- Pod Ready

## Storage

- PVC Bound
- Volume Mounted

## Network

- Service Endpoint
- Ingress Endpoint

## Application

- HTTP GET
- TCP Connection

# 15. 页面：迁移报告

展示：

- Source
- Target
- Resources
- PVC
- Pods
- Transferred
- Validation
- Duration
- 迁移前问题数
- 自动修复数
- 人工处理数

MVP 支持下载 JSON Report。

# 16. Design Token

## Primary Color

```css
--color-primary-50:  #EAF7FF;
--color-primary-100: #D5EFFF;
--color-primary-200: #AADFFF;
--color-primary-300: #75C8FF;
--color-primary-400: #3FB1FF;
--color-primary-500: #0096FF;
--color-primary-600: #007DD6;
--color-primary-700: #0064AD;
--color-primary-800: #004B83;
--color-primary-900: #00365F;
```

Primary Button：

```css
background: #0096FF;
```

Hover：

```css
background: #007DD6;
```

## Neutral

```css
--color-bg-page:     #F7F8FA;
--color-bg-card:     #FFFFFF;
--color-bg-subtle:   #F4F6F8;

--color-border:      #E4E7EB;
--color-border-dark: #D5DAE0;

--color-text-primary:   #1F2937;
--color-text-secondary: #5B6573;
--color-text-tertiary:  #8A94A3;
--color-text-disabled:  #B4BBC4;
```

视觉比例建议：

```text
蓝 10–15%
白 55–65%
灰 25–35%
```

不要大面积蓝色背景。

## Semantic

```css
--color-success: #2DA771;
--color-warning: #E79B24;
--color-error:   #D94A4A;
--color-info:    #0096FF;
```

# 17. Typography

```css
font-family:
  Inter,
  "PingFang SC",
  "Microsoft YaHei",
  Arial,
  sans-serif;
```

字号：

- Page Title：24px / 600
- Section Title：18px / 600
- Card Title：16px / 500
- Body：14px / 400
- Table：14px / 400
- Secondary：13px / 400
- Caption：12px / 400

禁止使用 32px+ 的巨大标题。

# 18. Spacing

```css
--space-1: 4px;
--space-2: 8px;
--space-3: 12px;
--space-4: 16px;
--space-5: 20px;
--space-6: 24px;
--space-8: 32px;
```

- 页面 Padding：24px
- Card 间距：16px

# 19. Radius

```css
--radius-small: 4px;
--radius-medium: 6px;
--radius-large: 8px;
```

- Button：6px
- Card：6px
- Modal：8px

禁止 16px+ 大圆角。

# 20. Shadow

Card 默认无阴影，使用 Border。

仅允许 Dropdown、Popover、Modal 使用轻微 Shadow：

```css
box-shadow: 0 4px 12px rgba(31,41,55,0.08);
```

# 21. Table

- Row Height：48px
- Header：#F7F8FA
- Border：#E4E7EB

不要给所有 Table 添加竖线。

# 22. Button

Primary：

- 开始迁移
- 创建迁移
- 确认
- 保存

Secondary：

- 取消
- 返回
- 重新检查

Danger：

- 删除
- 取消迁移

原则：

> 每个页面最多一个主要 Primary Button。

# 23. Card

禁止：

- 彩色 Icon + 大数字 + 渐变
- 大面积色块
- 营销式 Card

使用白底、细边框、简单层级。

# 24. Icon

建议：

- Lucide
- Ant Design Icons

统一 Outline Icon。

尺寸：

- 16px
- 20px

禁止 Emoji、3D Icon、彩色插画 Icon。

# 25. Progress

普通任务使用 Progress Bar。

迁移详情必须：

```text
Vertical Timeline
+
Progress
```

禁止只显示单一百分比。

# 26. 风险 Badge

用户文案：

- 阻塞
- 风险
- 提示

采用：

- 浅背景
- 深文字
- 小图标

禁止高饱和整块红底。

# 27. UI 明确禁止事项

Codex 不得设计：

- Gradient
- Glassmorphism
- Neon
- 大面积 Dark Blue
- 巨大圆角
- 彩色 Dashboard
- 3D Icon
- Emoji
- Hero Banner
- 宣传语
- 营销文案
- 大量动画
- 每个 Card 不同颜色
- Kubernetes Logo 满天飞
