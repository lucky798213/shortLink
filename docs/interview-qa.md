# 短链接服务面试问答

> 使用方式：每个问题都用 `<details>` 折叠块组织，答案默认隐藏。面试前可以先只看问题，点开小箭头再看参考回答。
>
> 说明：以下回答基于当前代码真实实现，不把计划中的理想设计说成已经落地的能力。遇到未完全实现的点，会直接说明现状和可优化方向。

## 一、架构与部署

<details>
<summary>HTTP 层（Gin）和核心服务层（gRPC）之间是进程内调用还是跨进程调用？如果是跨进程，etcd 注册的是 gRPC 服务的地址，Gin 层作为 gRPC client 是怎么做负载均衡的？</summary>

**回答：**

当前实现是跨进程调用。Web 层是一个独立的 Gin HTTP 服务，RPC 层是独立的 gRPC 服务，Web 不直接访问 MySQL，而是通过 gRPC client 调用 RPC 服务完成创建、查询、跳转、删除和统计。

如果启用 etcd，RPC 服务启动后会把自己的 gRPC 地址注册到 etcd，注册 key 的前缀是 `/short_url/services/{service_name}/`，value 是实例地址。Web 启动时会注册自定义 gRPC resolver，把 gRPC target 从固定地址切换成 `etcd:///short-url-rpc` 这种形式。resolver 会 watch etcd 服务前缀，拿到所有 RPC 实例地址后调用 `UpdateState` 更新 gRPC 客户端连接池，并通过 gRPC 的 `round_robin` 负载均衡策略在多个 RPC 实例之间轮询。

如果 etcd 不可用或配置关闭，Web 会回退到配置里的固定 `grpc.addr`，保证本地单机开发不依赖服务发现。

**代码位置：**

- `cmd/web/main.go`：Web 选择固定 gRPC 地址或 etcd resolver target，并创建 Handler。
- `internal/transport/httpserver/api.go`：Gin Handler 内部持有 `proto.ShortUrlClient` 和 gRPC health client。
- `cmd/rpc/main.go`：RPC 启动 gRPC server，并在 etcd 开启时注册服务地址。
- `internal/platform/discovery/etcd.go`：RPC 服务注册，使用 etcd lease + keepalive。
- `internal/platform/discovery/etcd.go`：自定义 gRPC resolver 解析服务地址并配置 `round_robin`。
- `internal/platform/discovery/etcd.go`：watch etcd 前缀，实例变化时更新客户端地址列表。

</details>

<details>
<summary>优雅关闭的具体实现是什么？收到 SIGTERM 后，正在处理的请求怎么等待完成，最长等待多少时间？</summary>

**回答：**

Web 服务和 RPC 服务都监听 `SIGINT` / `SIGTERM`。

Web 层收到信号后，会调用 `http.Server.Shutdown`，并设置 5 秒超时。`Shutdown` 会先停止接收新连接，然后等待已经进入 Handler 的请求处理完成；如果 5 秒内没有处理完，就会返回超时错误并记录告警。因此 Web 层最长等待 5 秒。

RPC 层收到信号后，会先把标准 gRPC health service 状态设置为 `NOT_SERVING`，然后调用 `grpc.Server.GracefulStop()` 停止接收新 RPC 并等待已有 RPC 完成。等待上限为 5 秒，超时后调用 `Stop()` 强制关闭。随后应用服务会继续在 5 秒关闭窗口内排空创建队列和访问日志队列，再关闭 Redis 与数据库连接。

**代码位置：**

- `cmd/web/main.go`：注册 `SIGINT` / `SIGTERM` 信号上下文。
- `cmd/web/main.go`：Web 收到信号后用 5 秒超时执行 `server.Shutdown`。
- `cmd/rpc/main.go`：RPC 注册 `SIGINT` / `SIGTERM` 信号上下文。
- `cmd/rpc/main.go`：注册标准 gRPC health service。
- `cmd/rpc/main.go`：RPC 收到信号后设置 `NOT_SERVING` 并执行 `GracefulStop()`。

</details>

<details>
<summary>Nginx 在这里承担什么职责？是纯反向代理还是还做了 SSL 终止、限流等？</summary>

**回答：**

当前 Nginx 主要承担三类职责：反向代理、连接复用、IP 级限流。

它把外部 `:8888` 流量转发到内部 Web 服务 `web:8080`，同时设置 `Host`、`X-Real-IP`、`X-Forwarded-For`、`X-Forwarded-Proto` 等反代头，便于 Web 层获取真实客户端信息。Nginx 还配置了 upstream keepalive，减少到 Web 服务的连接建立成本。

限流方面，Nginx 使用 `limit_req_zone` 按客户端 IP 做第一层粗粒度限流，配置是每个 IP `100r/s`，突发 `burst=200`。它是网关层保护，Web 层还有 Redis Lua 令牌桶做应用层限流。

当前没有配置 SSL 终止，Nginx 只监听 HTTP 80。生产环境如果需要 HTTPS，可以在 Nginx 上加证书并做 TLS termination。

**代码位置：**

- `nginx/nginx.conf:6-9`：upstream 指向 `web:8080`，并开启 keepalive。
- `nginx/nginx.conf:11`：按 IP 配置 `limit_req_zone`。
- `nginx/nginx.conf:17-29`：健康检查转发。
- `nginx/nginx.conf:31-40`：主反向代理、限流和转发头。
- `docker-compose.yaml`：Nginx 服务暴露 `:8888`。

</details>

## 二、号段分配器

<details>
<summary>号段分配器是单点还是分布式？如果是单点，它挂了怎么办，有备份吗？</summary>

**回答：**

应用层的号段分配器不是一个独立单点服务，而是每个 RPC 实例内部都有一个 `MySQLIDAllocator`。多个 RPC 实例之间通过 MySQL 的 `short_url_id_alloc` 表来协调全局 ID 分配。

具体做法是：每次领取号段时，在 MySQL 事务里对 `short_url_id_alloc` 的同一行执行 `SELECT ... FOR UPDATE`，拿到当前 `next_id` 后推进到 `end + 1`。MySQL 行锁保证同一时刻只有一个实例能成功领取某个号段，所以不会分配重复 ID。

因此，这里的分布式协调依赖 MySQL。RPC 实例可以水平扩容，某个 RPC 实例挂了不会影响其他实例继续领取号段；但 MySQL 本身是关键依赖。如果 MySQL 不可用，创建短链会失败。生产环境一般会通过 MySQL 主从、高可用或者云数据库来解决这个依赖的可用性问题。

**代码位置：**

- `internal/infra/mysql/id_allocator.go`：`MySQLIDAllocator` 结构体，实例内缓存当前号段。
- `internal/infra/mysql/id_allocator.go`：事务内插入 allocator 行、行锁读取、推进 `next_id`。
- `scripts/mysql/init.sql`：创建 `short_url_id_alloc` 表。
- `cmd/rpc/main.go`：RPC 初始化 MySQL 号段分配器并注入分片仓库。

</details>

<details>
<summary>一次分配多大的号段？号段用完触发续号时，续号请求是同步阻塞业务请求还是提前异步续号？</summary>

**回答：**

默认一次领取 1000 个 ID，配置项是 `id_allocator.step`。RPC 实例把领取到的 `[start, end]` 号段缓存在内存里，创建短链时直接从内存递增取 ID，只有当前号段耗尽时才会再次访问 MySQL 续领。

当前续号是同步触发的：当某个创建请求发现内存号段已经用完，会在这个请求路径上同步执行 `NextRange` 领取下一个号段。也就是说，触发续号的那个请求会多一次 MySQL 事务开销，后续请求继续走内存分配。

这版没有做提前异步续号。可以优化成“剩余号段低于阈值时后台预取下一段”，这样能降低少数续号请求的尾延迟。

**代码位置：**

- `configs/rpc.yaml`：默认 `id_allocator.step: 1000`。
- `cmd/rpc/main.go`：代码默认值也是 1000。
- `internal/infra/mysql/id_allocator.go`：构造 allocator，step 为 0 时回退 1000。
- `internal/infra/mysql/id_allocator.go`：`NextID` 内存号段未耗尽时直接返回，耗尽时同步调用 `NextRange`。
- `internal/infra/mysql/id_allocator.go`：从 MySQL 同步领取新号段。

</details>

<details>
<summary>服务重启后，内存里还没用完的号段会不会丢失？丢失了会导致什么问题（短码空洞），能接受吗？</summary>

**回答：**

会丢失。因为每个 RPC 实例领取到号段后，只把当前使用进度保存在进程内存里；服务重启后，内存中尚未使用的 ID 不会回写 MySQL。

这会导致 ID 和短码出现空洞，例如实例领取了 `1001-2000`，只用了 `1001-1200` 就重启了，那么 `1201-2000` 这段短码不会再被使用。这个问题不会导致 ID 冲突，也不会导致错误跳转，只是浪费一小段短码容量。

对短链接系统来说，6 位 Base62 容量约 568 亿，号段空洞通常可以接受。相比把未使用 ID 回收再复用，保留空洞能让系统更简单，也避免复杂回收带来的重复分配风险。

**代码位置：**

- `internal/infra/mysql/id_allocator.go`：`nextID` 和 `rangeEnd` 是内存字段。
- `internal/infra/mysql/id_allocator.go`：从内存范围递增返回 ID。
- `internal/infra/mysql/id_allocator.go`：领取号段时 MySQL 的 `next_id` 已提前推进到 `end + 1`，重启后不会回退。

</details>

<details>
<summary>id % 64 分表，短码和分表路由是怎么对应的？查询时如何知道去哪张分表查？</summary>

**回答：**

短码是由 ID 通过 Base62 编码得到的，而且编码是可逆的。创建时先从号段分配器拿到全局 ID，再用 `code.Encode(id)` 生成 6 位短码，然后按 `id % 64` 写入 `short_urls_00` 到 `short_urls_63` 中的一张表。

查询时不需要扫 64 张表。服务先校验短码是否合法，再用 `code.Decode(shortCode)` 反解出 ID，然后用同样的 `id % 64` 规则计算目标分表，最后在这张表里按 `id + short_code + is_deleted = 0` 查询。

这个设计的关键点是短码和 ID 可逆，查询时可以直接定位物理表。

**代码位置：**

- `internal/shortlink/code/code.go`：ID 编码为 6 位 Base62 短码。
- `internal/shortlink/code/code.go`：短码反解为 ID，并做长度和字符集校验。
- `internal/infra/mysql/sharding/sharding.go`：`TableByID` 使用 `id % shardCount`，`TableByShortCode` 先解码再路由。
- `internal/shortlink/app/creator.go` 和 `internal/shortlink/app/create_buffer.go`：领取 ID、生成短码并组装完整写入数据。
- `internal/infra/mysql/sharded_link_store.go`：查询时由短码定位单张分表。

</details>

## 三、分层读链路

<details>
<summary>LocalCache 是用什么实现的（sync.Map、groupcache、ristretto 还是自己写的 LRU）？LocalCache 和 Redis 之间的一致性怎么保证，Redis 更新了怎么通知各实例的 LocalCache 失效？</summary>

**回答：**

当前 LocalCache 是项目里自己实现的轻量本地缓存，不是 `sync.Map`、groupcache、ristretto，也不是严格 LRU。它内部是 `map[string]localEntry + sync.RWMutex`，每条记录带过期时间。如果超过最大容量，会删除 map 中遍历到的第一个 key，这只是简单容量保护，不保证 LRU 淘汰。

一致性策略是 TTL + 删除时双删。创建或查询回源后会写 Redis 和 LocalCache；删除短链时会先删 LocalCache 和 Redis，再删数据库，之后再删一次缓存，降低并发读到旧值的概率。

目前没有实现跨实例 LocalCache 主动失效通知。也就是说，一个 Web/RPC 实例删除短链时，只能删除自己进程内的 LocalCache 和 Redis；其他 RPC 实例的 LocalCache 需要等 TTL 到期才会失效。当前默认本地缓存 TTL 是 5 分钟。生产上如果要更强一致性，可以增加 Redis Pub/Sub、消息队列或者缓存版本号机制，在删除或更新时广播失效事件。

**代码位置：**

- `internal/infra/cache/cache.go`：LocalCache 结构体，`map + RWMutex`。
- `internal/infra/cache/cache.go`：读取时检查本地 TTL。
- `internal/infra/cache/cache.go`：写入本地缓存和简单容量淘汰。
- `internal/shortlink/app/manager.go`：删除短链时执行缓存双删。
- `internal/shortlink/app/resolver.go`：读写 Redis、本地缓存和删除缓存。
- `configs/rpc.yaml`：缓存 TTL 和本地缓存容量配置。

</details>

<details>
<summary>Bloom Filter 是单机的还是用 Redis 的 bitmap 实现的分布式版本？服务重启后 Bloom Filter 怎么恢复，是持久化到磁盘还是从 DB 重建？</summary>

**回答：**

当前 Bloom Filter 是基于 Redis bitmap 的分布式版本，不是单机内存 Bloom。所有 RPC 实例共享 Redis 中的同一个 bitmap key，默认是 `short_url:bloom:active`。

创建短链成功后，会把短码写入 Bloom。服务启动后，如果启用了 Bloom 重建任务，会扫描所有分表里的有效短码，并批量写入 Redis Bloom。定时任务也会周期性扫描 DB，把当前活跃短码重新写入 Bloom。

它不是持久化到本地磁盘，而是依赖 Redis 自身持久化能力以及 DB 重建能力。当前实现是向同一个 Bloom key 追加写入，不是“双 buffer 新版本 + 原子切换”。删除或过期短码不会从 Bloom 中删除，因为 Bloom 删除容易引入 false negative；保留这些位只会产生 false positive，最多多一次回源 DB，不会误判存在。

另外，当前 Bloom 采用 fail-open 策略：如果 Bloom key 不存在或 Redis 出错，`Exists` 会返回可能存在，让请求继续走缓存和 MySQL，避免 Bloom 故障直接导致正常短链无法访问。

**代码位置：**

- `internal/platform/bloom/bloom.go`：Redis Bloom Filter 结构体和默认参数。
- `internal/platform/bloom/bloom.go`：批量 `SETBIT` 写入 Bloom。
- `internal/platform/bloom/bloom.go`：`GETBIT` 判断是否存在，Redis 异常或 key 不存在时 fail-open。
- `internal/shortlink/app/creator.go` 和 `internal/shortlink/app/create_buffer.go`：创建成功后写入 Bloom。
- `internal/shortlink/app/creator.go`：封装 Bloom 写入失败告警。
- `internal/jobs/maintenance.go`：扫描 DB 重建 Bloom。
- `configs/rpc.yaml`：Bloom 开关、key、bits、hashes 和重建周期。

</details>

<details>
<summary>读链路的层次顺序是 Bloom Filter → LocalCache → Redis → MySQL 吗？LocalCache miss 但 Redis hit 时，会回填 LocalCache 吗？</summary>

**回答：**

当前真实读链路不是 Bloom 最先，而是：

1. 短码格式校验：长度必须是 6，字符必须属于 Base62，并且能反解到容量范围内。
2. 查 LocalCache。
3. 查 Redis。
4. 查 Bloom Filter，Bloom 判断不存在时直接返回 not found 并写入负缓存。
5. singleflight 合并相同短码的并发回源请求。
6. singleflight 内再查一次 Redis，防止并发期间其他请求已经回填。
7. 回源 MySQL。
8. 将 DB 结果写回 Redis 和 LocalCache。

LocalCache miss 但 Redis hit 时，会回填 LocalCache。代码里 `getCache(ctx, remoteCache, shortCode, true)` 的 `warmLocal=true` 就是这个作用。

把 Bloom 放在 Redis 后面的好处是：已有热点数据如果已经在 LocalCache/Redis 中，可以不访问 Bloom，减少一次 Redis bitmap 操作；Bloom 主要拦截“缓存 miss 后准备回源 DB 的无效短码”。

**代码位置：**

- `internal/shortlink/app/resolver.go`：短码校验、LocalCache、Redis、Bloom、singleflight 和 MySQL 回源。
- `internal/shortlink/app/resolver_test.go`：验证首个调用方取消不会取消共享回源。

</details>

<details>
<summary>Redis 异常降级时，流量直接打到 MySQL，你有没有评估过 MySQL 能承受的 QPS 上限，有没有兜底的限流？</summary>

**回答：**

当前设计里 Redis 缓存读取失败会记录告警并继续回源 MySQL，这属于可用性优先的降级策略。Bloom Filter 也是 fail-open，Redis 或 Bloom key 异常时不会直接拒绝请求。

兜底保护主要在入口层：Nginx 有 IP 级限流，Web 层有 Redis Lua 令牌桶限流；如果 Redis 限流不可用，启动阶段会降级为进程内令牌桶，运行时 Redis 限流异常则 fail-open 并记录告警。Web 到 RPC 的调用还包了一层 Hystrix 熔断，RPC 异常或超时会快速失败，避免请求无限堆积。

需要坦诚的是：当前没有完成真实 MySQL 集成压测，因此不能声称已经验证过 MySQL 在 Redis 全挂场景下的极限 QPS。已有本地 benchmark 是 Mock RPC / LocalCache 命中链路，不能代表 DB 回源能力。生产上应该补充 DB 回源压测，并基于 MySQL CPU、连接数、慢查询和 P99 延迟来校准入口限流阈值。

**代码位置：**

- `internal/shortlink/app/resolver.go`：缓存读取异常时告警并回源。
- `internal/platform/bloom/bloom.go`：Bloom 异常或 key 不存在时 fail-open。
- `nginx/nginx.conf:11`、`nginx/nginx.conf:31-32`：Nginx IP 级限流。
- `cmd/web/main.go`：Web 限流器初始化，Redis 不可用时降级到内存令牌桶。
- `internal/transport/httpserver/middleware/middleware.go`：运行时限流失败时放行并记录告警。
- `cmd/web/main.go`：Hystrix 命令配置。

</details>

## 四、写链路

<details>
<summary>Channel 缓冲批量提交，batch size 和 flush interval 是怎么设置的？两个条件（满了 / 超时）哪个先触发就提交？</summary>

**回答：**

创建短链的写缓冲使用 channel 承接请求，默认队列大小是 10000，batch size 是 128，flush interval 是 10ms，入队超时是 200ms。

请求进来后会先进入 channel，然后同步等待本批次的写入结果。后台 goroutine 不断从 channel 取请求，如果当前 batch 达到 128 条，会立即 flush；如果没有满，但 10ms ticker 到了，也会 flush。也就是说“满了”和“超时”谁先到就先提交。

如果 channel 在 200ms 内无法入队，创建请求会返回超时错误。这样可以避免在突发流量下请求无限堆积。

**代码位置：**

- `configs/rpc.yaml`：写缓冲配置。
- `cmd/rpc/main.go`：默认配置值。
- `internal/shortlink/app/create_buffer.go`：创建 buffer，设置默认 queue、batch、flush interval 和 enqueue timeout。
- `internal/shortlink/app/create_buffer.go`：请求入队并同步等待写入结果。
- `internal/shortlink/app/create_buffer.go`：batch 满或 ticker 到期就 flush。

</details>

<details>
<summary>批量写入 MySQL 用的是 INSERT INTO ... VALUES (...),(...)，还是批量事务？如果批次中有一条数据违反唯一约束，整批怎么处理？</summary>

**回答：**

当前批量写入是用 GORM 的 `Create(&slice)`，GORM 会生成多 values 的批量 insert。因为已经做了 64 分表，批次会先按目标分表分组，然后每张表分别执行一次批量插入。

所有分片批次都包在同一个 GORM/MySQL 事务中。任意一张分片表的批量插入失败，整个事务回滚，已经写入的其他分片不会留下部分数据；CreateBuffer 会把同一个失败结果返回给本批次所有等待请求。号段 ID 负责正常路径下的全局唯一性，事务负责异常路径下的批次原子性。

**代码位置：**

- `internal/infra/mysql/sharded_link_store.go`：按分表分组后 GORM 批量插入。
- `internal/shortlink/app/create_buffer.go`：flush 时分配 ID、组装 rows、调用 `BatchCreate`，失败后把错误返回给等待中的请求。
- `internal/shortlink/code/code.go`：6 位短码容量边界。
- `internal/infra/mysql/id_allocator.go`：号段分配保证全局 ID 不重复。

</details>

<details>
<summary>访问日志也是批量写入的，日志写入失败（比如 MySQL 满了）会影响跳转主链路吗，怎么隔离的？</summary>

**回答：**

访问日志不会阻塞跳转主链路。跳转接口在拿到短链映射后，会把访问记录尝试写入内存 channel，然后立即返回跳转响应。后台 worker 批量消费 channel，把访问日志批量写入 MySQL。

如果访问日志队列满了，当前请求的访问日志会被丢弃并记录 warn 日志，但不会影响跳转。如果后台批量写 MySQL 失败，也只会记录告警，不会反向影响已经完成的跳转请求。

这个设计牺牲了少量统计完整性，换取跳转链路稳定性和低延迟。短链接跳转是核心链路，访问日志是旁路统计链路，优先级更低。

**代码位置：**

- `internal/shortlink/app/service.go`：跳转查询成功后把访问事件交给 VisitWriter。
- `internal/shortlink/app/visit_writer.go`：访问日志入队、队列满降级、批量落库和关闭排空。
- `internal/infra/mysql/visit_store.go`：访问日志批量写入 MySQL。
- `configs/rpc.yaml`：访问日志队列、worker、batch 和 flush interval 配置。

</details>

## 五、统计与隐私

<details>
<summary>UV 统计是怎么去重的？用 Redis HyperLogLog 还是 Bitmap？跨天的 UV 怎么处理？</summary>

**回答：**

当前 UV 统计不是 Redis HyperLogLog，也不是 Bitmap，而是基于 MySQL 访问日志表做实时聚合：对某个短码执行 `COUNT(DISTINCT ip)`。这里的 `ip` 不是明文 IP，而是加盐 SHA256 后的哈希值，所以同一个 IP 在相同 salt 下会得到相同 hash，可以用于去重。

当前没有实现按天 UV。现在统计的是某个短码全量访问日志范围内的 PV / UV。如果要支持跨天 UV，可以在访问日志里增加日期维度索引，查询时按 `created_at` 范围过滤；更高性能的做法是每天维护一个 Redis HyperLogLog key，例如 `uv:{short_code}:{yyyyMMdd}`，跨天时对多个 HLL 做 union 或按天展示。

**代码位置：**

- `internal/infra/mysql/visit_store.go`：PV 和 UV 查询，UV 使用 `COUNT(DISTINCT ip)`。
- `internal/shortlink/app/visit_writer.go`：入队访问日志时保存加盐 SHA256 后的 IP。
- `scripts/mysql/init.sql`：访问日志表结构。

</details>

<details>
<summary>Top Referer / Top UA 统计是实时的还是离线计算的？数据结构用的是 Redis Sorted Set 吗？</summary>

**回答：**

当前 Top Referer 和 Top User-Agent 是实时 SQL 聚合，不是离线计算，也没有使用 Redis Sorted Set。

查询统计接口时，会直接在 `short_url_visits` 表里按 `referer` 或 `user_agent` 分组，计算 `COUNT(*)`，再按 count 倒序取 TopN。这个实现简单、数据准确，但当访问日志量很大时，统计接口会比较依赖 MySQL 聚合性能。

如果要优化，可以把访问时的 Referer / UA 计数同步或异步写到 Redis Sorted Set，例如 `zincrby stats:referer:{short_code}`，查询 TopN 时用 `ZREVRANGE`。也可以把访问日志作为明细保留，Top 统计走异步离线任务或流式聚合。

**代码位置：**

- `internal/infra/mysql/visit_store.go`：Top Referer 实时 SQL 聚合。
- `internal/infra/mysql/visit_store.go`：Top User-Agent 实时 SQL 聚合。
- `internal/shortlink/app/stats.go`：统计接口先确认短链存在，再查询访问统计。

</details>

<details>
<summary>IP SHA256 哈希之后还能做 UV 去重吗，哈希碰撞对统计结果有影响吗？为什么选 SHA256 而不是更轻量的哈希？</summary>

**回答：**

可以做 UV 去重。UV 只需要“同一个用户标识能稳定映射到同一个值”，不一定需要明文 IP。当前实现是 `salt + ip` 做 SHA256，同一个 IP 在同一个 salt 下每次得到的 hash 一样，所以可以用 `COUNT(DISTINCT ip_hash)` 做去重。

哈希碰撞理论上可能导致两个不同 IP 被算成一个 UV，但 SHA256 的输出空间是 256 bit，碰撞概率可以认为极低，对这个业务的统计结果几乎没有影响。

选择 SHA256 主要是隐私考虑：它是密码学哈希，比 MurmurHash、FNV 这类非密码学哈希更难反推原始 IP；再加上 salt，安全性更好。性能上 SHA256 比轻量哈希慢，但访问日志写入是异步旁路，且每次只 hash 一个 IP，对跳转主链路影响很小。后续如果统计量极大，也可以评估用 SipHash / xxHash 加密化方案平衡性能和隐私。

**代码位置：**

- `internal/shortlink/app/visit_writer.go`：`salt + ip` 做 SHA256、hex 编码并写入访问事件。
- `internal/infra/mysql/visit_store.go`：基于 hash 后 IP 做 `COUNT(DISTINCT ip)`。
- `configs/rpc.yaml`：`stats.ip_hash_salt` 配置。

</details>

## 六、限流与熔断

<details>
<summary>令牌桶限流是按接口维度还是按 IP 维度？Lua 脚本里的 key 设计是什么样的，TTL 怎么处理？</summary>

**回答：**

当前 Web 层限流按“流量类型 + IP”维度区分。路径以 `/api/` 开头的管理接口走 API 限流器，跳转链路走 redirect 限流器。key 里包含 scope 和客户端 IP，例如 `api:127.0.0.1` 或 `redirect:127.0.0.1`。

Redis 令牌桶实际 key 由 limiter prefix 和业务 key 拼接。API 限流器 prefix 是 `short_url:rate_limit:api:`，redirect 限流器 prefix 是 `short_url:rate_limit:redirect:`。Lua 脚本用 Redis Hash 保存两个字段：`tokens` 表示当前令牌数，`ts` 表示上次刷新时间。每次请求按时间差补充令牌，最多不超过 burst；如果剩余令牌大于等于本次请求消耗，就扣 1 个令牌并放行。

TTL 当前固定为 1 分钟，每次访问都会 `PEXPIRE` 更新 TTL。这样不活跃 IP 的令牌桶 key 会自动过期，避免 Redis key 无限增长。

**代码位置：**

- `internal/transport/httpserver/middleware/middleware.go`：按 `/api/` 区分 API / redirect，并用 `scope + IP` 作为限流 key。
- `internal/platform/ratelimit/limiter.go`：Redis Lua 令牌桶脚本。
- `internal/platform/ratelimit/limiter.go`：Redis limiter 默认参数和 1 分钟 TTL。
- `internal/platform/ratelimit/limiter.go`：执行 Lua 脚本并返回是否允许。
- `cmd/web/main.go`：API 和 redirect limiter 的 Redis key prefix。

</details>

<details>
<summary>令牌桶的补充速率和桶容量是怎么定的，有没有做过压测来校准这两个参数？</summary>

**回答：**

当前默认配置是：API 管理接口每 IP 每秒补充 50 个令牌，桶容量 100；跳转接口每 IP 每秒补充 500 个令牌，桶容量 1000。这样设计是因为跳转链路是高频核心链路，创建、删除、统计这类管理接口频率相对低。

目前这些阈值是经验默认值，还没有通过完整 Docker Compose + MySQL + Redis + etcd 的端到端压测校准。之前本地能跑通的是 Go benchmark：HTTP Handler 在 Mock RPC 场景下，跳转约 14.8 万 QPS，创建约 12.6 万 QPS；Service 层 LocalCache 热点读取约 1030 万 QPS。但这些数据不包含真实网络、Redis、MySQL 写入和 Nginx，不适合直接作为生产限流阈值。

真实上线前应该用 wrk / vegeta / k6 做端到端压测，分别测试缓存命中、缓存 miss、DB 回源、创建写入和 Redis 异常场景，再根据 P99 延迟、MySQL CPU、连接数、慢查询、Redis RTT 和错误率来调整 rate 和 burst。

**代码位置：**

- `configs/web.yaml`：限流默认参数。
- `cmd/web/main.go`：代码默认限流值。
- `cmd/web/main.go`：按配置创建 Redis 或内存令牌桶。
- `scripts/wrk/create.lua`：创建短链压测脚本，目标接口是 `/api/short-links`。
- `internal/transport/httpserver/benchmark_test.go`：HTTP Handler benchmark。
- `internal/shortlink/app/benchmark_test.go`：Service LocalCache benchmark。

</details>

<details>
<summary>Hystrix 熔断是用的哪个 Go 实现（afex/hystrix-go 还是其他）？熔断打开到 half-open 的等待时间是多少，half-open 放多少请求进来探测？</summary>

**回答：**

当前使用的是 `github.com/afex/hystrix-go/hystrix`。Web 层调用 RPC 时，每类接口都包在独立的 Hystrix command 中，包括 create、get、delete、stats、redirect。

默认配置是：超时 2500ms，最大并发 100，请求量阈值 20，错误率阈值 50%，熔断打开后的 sleep window 是 5000ms。也就是说，在一个统计窗口内请求量达到阈值后，如果错误率超过 50%，熔断器会打开；打开后等待 5 秒进入可探测状态。

`hystrix-go` 的 half-open 探测机制不是我们自己手写的。它在 sleep window 之后允许少量请求尝试通过，如果探测成功就关闭熔断器，如果失败则重新打开。面试时可以说“half-open 探测请求数量由 hystrix-go 内部控制，我这里没有单独配置并发探测数”。

**代码位置：**

- `go.mod`：依赖 `github.com/afex/hystrix-go`。
- `configs/web.yaml`：Hystrix 配置项。
- `cmd/web/main.go`：Hystrix 默认配置。
- `cmd/web/main.go`：为 create/get/stats/delete/redirect 配置 Hystrix command。
- `internal/transport/httpserver/api.go`：创建接口使用 `hystrix.DoC`。
- `internal/transport/httpserver/api.go`：详情查询接口使用 `hystrix.DoC`。
- `internal/transport/httpserver/api.go`：统计接口使用 `hystrix.DoC`。
- `internal/transport/httpserver/api.go`：删除接口使用 `hystrix.DoC`。
- `internal/transport/httpserver/api.go`：跳转接口使用 `hystrix.DoC`。

</details>

<details>
<summary>Redis 挂了之后，令牌桶限流失效，这时候熔断器还能正常工作吗？你的降级兜底是什么？</summary>

**回答：**

Redis 挂了不影响 Hystrix 熔断器工作，因为 Hystrix 是 Web 进程内的 RPC 调用保护，和 Redis 限流不是同一个依赖。

限流的降级分两种情况：如果 Web 启动时 Redis ping 不通，会直接创建进程内令牌桶作为替代；如果运行过程中 Redis 令牌桶执行 Lua 脚本失败，当前中间件会记录 warn 日志并放行请求，也就是 fail-open。

fail-open 的好处是 Redis 短暂抖动不会直接造成全站 429；坏处是 Redis 持续不可用时，分布式限流会退化。这个时候还有 Nginx 的 IP 级限流和 Hystrix 的并发、超时、错误率保护来兜底。更严格的生产策略可以改成 Redis 运行时失败后自动切换本机内存令牌桶，或者对核心接口 fail-open、对管理接口 fail-closed。

**代码位置：**

- `cmd/web/main.go`：启动时 Redis 不可用则降级到内存令牌桶。
- `internal/transport/httpserver/middleware/middleware.go`：运行时限流器报错时记录日志并放行。
- `internal/platform/ratelimit/limiter.go`：进程内令牌桶实现。
- `cmd/web/main.go`：Hystrix 作为 RPC 调用保护。
- `nginx/nginx.conf:11`、`nginx/nginx.conf:31-32`：Nginx IP 级兜底限流。

</details>

## 七、可观测性

<details>
<summary>Zap 日志是写到标准输出还是文件？日志轮转怎么处理的？</summary>

**回答：**

当前 Zap 日志写到标准输出，不写应用本地文件。`internal/platform/logging` 初始化了全局 Zap logger，并把 Go 标准 `slog` 的默认 handler 桥接到 Zap，这样项目里原来用 `slog` 的地方也会走统一结构化日志。

日志轮转当前不在应用内处理，而是交给容器运行时、Docker logging driver 或部署平台处理。这是容器化服务常见做法：应用只负责输出结构化日志到 stdout/stderr，日志采集、落盘、轮转和保留周期由外部平台处理。

如果部署在裸机并要求本地文件，可以引入 lumberjack 做文件切割；但在 Docker / k8s 环境里，stdout 更符合平台日志采集习惯。

**代码位置：**

- `internal/platform/logging/logging.go`：按配置初始化 Zap production/development logger。
- `internal/platform/logging/logging.go`：设置全局 Zap logger，并把 `slog` 桥接到 Zap。
- `internal/platform/logging/logging.go`：默认 logger 输出到 `os.Stdout`。
- `cmd/web/main.go`、`cmd/rpc/main.go`：Web/RPC 启动时初始化日志。
- `configs/web.yaml`、`configs/rpc.yaml`：日志配置。

</details>

<details>
<summary>request_id 在跳转链路（HTTP → gRPC → MySQL / Redis）全程是怎么透传的？gRPC 里是放在 metadata 里吗？</summary>

**回答：**

当前 request_id 主要在 HTTP 层使用，还没有完整透传到 gRPC metadata、Redis 和 MySQL 日志里。

HTTP 请求进来后，中间件会优先读取 `X-Request-ID`，如果没有就生成一个 16 字节随机 ID。这个 request_id 会写回响应头，并放进 Gin context，用于 HTTP 访问日志和统一错误响应。因此客户端能拿到 request_id，HTTP 层日志也能按 request_id 检索。

但当前 Web 调 RPC 时没有把 request_id 写入 gRPC metadata，RPC 服务日志也没有从 metadata 里取 request_id。所以严格说还没有做到 HTTP → gRPC → DB/Redis 的全链路 trace。可以优化为：Web 调 RPC 前用 `metadata.AppendToOutgoingContext(ctx, "x-request-id", requestID)` 注入；RPC server interceptor 从 incoming metadata 读取并放进 context/logger 字段；DB/Redis 操作日志也带上这个字段。

**代码位置：**

- `internal/transport/httpserver/middleware/middleware.go`：统一错误响应结构包含 request_id。
- `internal/transport/httpserver/middleware/middleware.go`：JSON 错误响应写入 request_id。
- `internal/transport/httpserver/middleware/middleware.go`：生成或读取 `X-Request-ID`，并写入响应头。
- `internal/transport/httpserver/middleware/middleware.go`：HTTP 访问日志带 request_id。
- `internal/transport/httpserver/api.go`、`internal/transport/httpserver/api.go`：RPC 调用使用 HTTP request context，但当前没有注入 gRPC metadata。

</details>

<details>
<summary>有没有接入指标监控（Prometheus / metrics）？如果没有，出了问题怎么排查性能瓶颈？</summary>

**回答：**

当前还没有接入 Prometheus metrics，也没有暴露 `/metrics`。已经有的是健康检查、结构化日志、gRPC health service、Hystrix 错误兜底和本地 benchmark。

如果线上排查性能瓶颈，当前可以先从几个方向入手：看 Web 结构化访问日志里的状态码、path、latency 和 request_id；看 RPC 日志里的缓存失败、Bloom 失败、访问日志队列满、批量写入失败；看 MySQL 慢查询、连接数和 CPU；看 Redis 延迟和错误；看 Hystrix 是否频繁返回 `rpc_unavailable`。

但这是“日志 + 外部组件指标”的排查方式，还不够系统。后续建议接入 Prometheus，至少补充这些指标：HTTP QPS、状态码、P95/P99 延迟、RPC 调用耗时、LocalCache/Redis 命中率、Bloom 拦截数、DB 回源次数、创建队列长度、访问日志队列丢弃数、Hystrix 熔断状态、MySQL 批量写耗时。

**代码位置：**

- `cmd/web/main.go`：Web 暴露 `/healthz` 和 `/readyz`。
- `internal/transport/httpserver/health.go`：Web 存活和就绪检查。
- `cmd/rpc/main.go`：RPC 注册标准 gRPC health service。
- `internal/transport/httpserver/middleware/middleware.go`：HTTP 结构化访问日志。
- `internal/shortlink/app/resolver.go`：缓存异常告警。
- `internal/shortlink/app/visit_writer.go`：访问日志队列满和批量写入失败告警。

</details>
