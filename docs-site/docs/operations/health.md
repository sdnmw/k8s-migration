---
title: 平台健康检查
sidebar_position: 1
---

# 平台健康检查

## Kubernetes 工作负载

```bash
kubectl -n sks-migration-center get deploy,statefulset,pod,svc,pvc
kubectl -n sks-migration-center get events --sort-by=.lastTimestamp
```

API、Worker、Web、PostgreSQL、MinIO 必须 Ready。PostgreSQL 日志出现 “database system is ready to accept connections” 后收到 fast shutdown，通常说明 Pod 被控制器、探针或升级流程终止，不是数据库自行崩溃；继续检查 Pod events、探针和 rollout。

## API 与 Web

- 平台首页应返回成功；API 内部 `/healthz` 检查进程、`/readyz` 检查数据库就绪（不是 Web 的 `/api/health`）。
- `/docs/` 必须由 Nginx 静态路由处理，刷新子页面不能回到平台首页。
- 文档浏览不应请求 CDN、在线字体、Algolia 或平台 API。

## Worker 心跳和租约

执行时序展示 Run 的当前 Step、最近心跳和等待原因。心跳停止且租约过期时，先查看 Worker 日志与外部组件，再重新执行。不要同时运行多个 Worker 手工处理同一 Run。
