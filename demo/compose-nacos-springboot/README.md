# Spring Boot + Nacos + PostgreSQL Compose 演示

该演示用于验证 Compose 到 SKS 的完整有状态迁移：

- Spring Boot 同时提供 Web 页面和 REST API。
- Spring Boot 通过 Nacos HTTP API 注册为 `sks-springboot-demo`。
- PostgreSQL 使用 `postgres-data` named volume 保存业务记录。
- Nacos 使用 `nacos-data` named volume 保存注册中心运行数据。
- 迁移前向 `/api/records` 写入唯一证明记录；迁移后比对记录集合和数据库摘要。
