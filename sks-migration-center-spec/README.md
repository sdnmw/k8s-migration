# SKS Migration Center

面向 SmartX SKS 的 Docker Compose / Kubernetes 应用可视化迁移工作台。

## 文档说明

本目录包含项目的核心产品与工程规格：

- `PRODUCT_SPEC.md`：产品定位、用户、核心迁移流程、功能边界
- `UI_SPEC.md`：页面结构、交互规范、SmartX 风格 Design Token
- `DOMAIN_MODEL.md`：后端领域对象、状态机、API、Adapter/Engine 边界
- `MVP_TASKS.md`：MVP 范围、开发顺序、验收标准、演示场景

## Codex 推荐读取顺序

1. 先阅读 `PRODUCT_SPEC.md`
2. 再阅读 `DOMAIN_MODEL.md`
3. 开发前端时阅读 `UI_SPEC.md`
4. 实施过程中严格以 `MVP_TASKS.md` 约束范围

## 一句话产品定义

> SKS Migration Center 不是 Velero 和 Kompose 的可视化界面，而是一套将 Docker Compose 和 Kubernetes 应用经过评估、转换、映射、迁移和验证后可靠迁移至 SmartX SKS 的迁移工作台。
