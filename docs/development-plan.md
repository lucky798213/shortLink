# 短链接项目开发文档

## 项目定位

本项目当前是一个短链接服务 MVP，目标是逐步升级为可以写进简历、能够清楚讲解架构取舍的 Go 后端项目。演进方向是先保证核心链路稳定，再增加缓存、防穿透、访问统计、限流和工程化能力。

## 当前已实现

- Gin HTTP 服务，对外提供创建短链接和短码重定向入口。
- gRPC RPC 服务，承载短链接创建和查询业务。
- MySQL 持久化短码与原始链接的映射关系。
- Redis 缓存短码映射，优化跳转和详情查询读路径。
- LocalCache + singleflight 保护热点短链和缓存失效回源。
- 异步访问日志与短链接访问统计。
- Base62 编码器，将自增 ID 转为固定 6 位短码。
- Docker Compose 基础编排 MySQL、RPC、Web 服务。
- `POST /api/short-links` 创建短链接，支持可选过期时间。
- `GET /api/short-links/:code` 查询短链接详情。
- `DELETE /api/short-links/:code` 软删除短链接。
- `GET /:code` 根据短码查询原始链接并返回 301 重定向；已过期短链返回 410。

## 第一阶段：基础链路稳定化

目标：把 MVP 做成稳定可用的基础版，避免创建流程留下脏数据，并让接口返回更贴近真实短链接服务。

To-do：

- [x] 修复 `short_code NOT NULL` 与“先插入、后更新短码”流程的冲突。
- [x] 新增已有数据库迁移脚本，允许 `short_code` 为 `NULL`。
- [x] 创建短链接流程加入事务，保证插入和更新短码整体提交或整体回滚。
- [x] 创建接口增加 `origin_url` 合法性校验，只允许 `http://` 和 `https://`。
- [x] 创建接口返回完整短链接 `short_url`，同时保留 `short_code` 兼容旧调用方。
- [x] 新增 `short_url.base_url` 配置，用于拼接完整短链接。
- [x] 增加 URL 校验和创建事务相关单元测试。

验收命令：

```bash
go test ./...
go build ./...
```

## 第二阶段：业务能力补全

目标：从“能创建和跳转”升级为“可管理的短链接系统”。

To-do：

- [x] 支持 `expire_at` 过期时间，跳转时过滤已过期短链。
- [x] 新增短链接详情查询接口 `GET /api/short-links/:code`。
- [x] 支持软删除短链接，复用 `is_deleted` 字段。
- [x] 迁移为 REST 管理接口 `POST /api/short-links`，不再注册旧的 `/api/create`。
- [x] 补充 Service、Repository、HTTP Handler 层测试。

当前接口：

```text
POST   /api/short-links
GET    /api/short-links/:code
DELETE /api/short-links/:code
GET    /:code
```

创建请求：

```json
{
  "origin_url": "https://example.com",
  "expire_at": 0
}
```

`expire_at` 使用 Unix 秒，`0` 或省略表示永不过期。访问已过期短链时返回 `410 Gone`，不存在或已软删除返回 `404 Not Found`。

## 第三阶段：Redis 缓存与防穿透

目标：优化高频跳转场景，降低数据库查询压力。

To-do：

- [x] Docker Compose 增加 Redis 服务。
- [x] RPC 服务初始化 Redis 客户端。
- [x] 查询短码时优先查 Redis，未命中再查 MySQL。
- [x] MySQL 查到后回写 Redis。
- [x] Redis TTL 与短链过期时间对齐。
- [x] 对不存在的短码写入短 TTL 空值缓存，缓解缓存穿透。

缓存策略：

```text
GET /:code 或 GET /api/short-links/:code
  -> 查询 Redis key: short_url:code:{short_code}
  -> 命中 active / expired / not_found，直接返回对应状态
  -> 未命中，查询 MySQL
  -> MySQL 查到后写入 Redis
  -> MySQL 查不到时写入 not_found 空值缓存
```

TTL 策略：

- 永不过期短链默认缓存 24 小时。
- 有 `expire_at` 的短链缓存到过期时间。
- 已过期短链缓存 `expired` 状态，默认 24 小时。
- 不存在短码缓存 `not_found` 状态，默认 1 分钟。

Redis 是缓存依赖，不是核心数据源。Redis 连接或读写失败时，RPC 服务会记录日志并降级查询 MySQL，避免缓存故障影响短链接核心链路。

## 第四阶段：热点保护与访问统计

目标：提升热点短链稳定性，并让项目具备数据分析能力。

To-do：

- [x] 使用 `singleflight` 合并同一短码的并发回源请求。
- [x] 增加本地缓存 LocalCache，缓存极热点短码。
- [x] 缓存 TTL 增加随机抖动，降低缓存雪崩风险。
- [x] 新增访问日志表，记录短码、IP Hash、User-Agent、Referer、访问时间。
- [x] 跳转时异步写访问日志，避免阻塞重定向主流程。
- [x] 新增短链接访问统计接口。

热点保护流程：

```text
GET /:code 或 GET /api/short-links/:code
  -> 查询 LocalCache
  -> 未命中查询 Redis
  -> 未命中使用 singleflight 合并同短码 MySQL 回源
  -> 回源成功后写入 Redis 和 LocalCache
```

访问统计接口：

```text
GET /api/short-links/:code/stats
```

响应字段：

```json
{
  "short_code": "000001",
  "pv": 123,
  "uv": 45,
  "last_visited_at": 1710000000,
  "top_referers": [{"value": "https://example.com", "count": 10}],
  "top_user_agents": [{"value": "Mozilla/5.0", "count": 8}]
}
```

统计策略：

- 只统计成功跳转的 `active` 短链访问。
- 访问日志通过异步 Channel 聚合批量写 MySQL，队列满时丢弃日志，优先保证跳转低延迟。
- IP 使用 SHA256 哈希后写入 `short_url_visits.ip`，用于 UV 计算，避免保存原始 IP。
- 统计聚合返回 PV、UV、最近访问时间、Top 5 Referer 和 Top 5 User-Agent。

## 第五阶段：安全、限流与工程化

目标：让服务具备基本生产治理能力。

To-do：

- [x] 创建短链接口增加可选 API Key 鉴权，默认关闭以兼容本地开发。
- [x] 增加 IP 级限流，优先使用 Redis Lua 令牌桶，Redis 不可用时降级内存令牌桶。
- [x] 增加健康检查接口 `GET /healthz` 和 `GET /readyz`，RPC 注册标准 gRPC health service。
- [x] 增加优雅关闭，HTTP 和 gRPC 收到退出信号后停止接收新请求并释放资源。
- [x] 统一错误响应格式，包含 `code`、`message` 和 `request_id`。
- [x] 增加 GitHub Actions，自动执行测试、构建和 vet。
- [x] 增加压测脚本 `scripts/wrk/create.lua`，使用当前 `POST /api/short-links` 接口。

## 第六阶段：分布式与高并发增强

目标：把目标架构中的防穿透、分表、服务发现和代理治理落到代码与部署物料。

To-do：

- [x] 短码增加 Decode 和合法性校验，非法短码在进入缓存/数据库前直接返回 not found。
- [x] 新增 64 张物理分表 `short_urls_00` 到 `short_urls_63`，按 `id % 64` 路由。
- [x] 新增 MySQL 号段分配表 `short_url_id_alloc`，创建短链时预先领取全局 ID。
- [x] 创建短链接入批量写入缓冲区，访问日志也改为批量落库。
- [x] 新增 Redis Bloom 防穿透，启动和定时任务扫描分表重建 Bloom，创建成功后增量写入。
- [x] 新增过期清理任务，定时软删除过期短链并清理缓存。
- [x] 新增 etcd 服务注册和发现，Web 通过 gRPC round_robin 负载均衡访问 RPC。
- [x] 新增 Hystrix 熔断配置，按创建、查询、统计、删除和跳转命令隔离。
- [x] 新增 Nginx 反向代理与 IP 级限流，Docker Compose 暴露 `:8888`。
- [x] 日志迁移到统一 Zap logger，并桥接 `slog` 调用。

## 简历表达方向

当前阶段完成后，可以真实描述为：

> 基于 Go 实现分层架构短链接系统，使用 Gin 对外提供 REST API，内部通过 gRPC 调用短链接服务，基于 MySQL 64 分表存储短码与原始链接映射，并使用 MySQL 号段分配 + Base62 编码生成固定长度短码。创建短链接流程通过批量写缓冲聚合落库，支持 URL 合法性校验、过期时间控制、详情查询和软删除。引入 Redis、LocalCache、singleflight 和 Redis Bloom，读路径先进行短码合法性校验和防穿透过滤，再逐级缓存命中，缓存故障时自动降级到 MySQL。跳转链路异步批量写访问日志，支持 PV、UV、Referer、User-Agent 等访问统计。Web 层具备 API Key、Redis/内存令牌桶限流、Hystrix 熔断、健康检查、优雅关闭和统一错误响应，Docker Compose 集成 etcd 服务发现与 Nginx 反向代理。
