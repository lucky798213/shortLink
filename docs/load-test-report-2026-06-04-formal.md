# ShortLink 正式压测报告

压测时间：2026-06-04 14:13 CST

证据目录：`docs/load-test-artifacts/20260604-141306`

报告结论：本报告基于本次重新执行的 60 秒端到端压测，原始 `bombardier` JSON、Docker 环境快照、资源限制、健康检查、日志扫描和 MySQL 访问日志计数均已落盘。

## 1. 压测目标

验证短链接服务在本地 Docker Compose 完整环境中的端到端表现，重点关注：

- 短链跳转链路：`GET /:code`，成功状态码为 `301`。
- 短链创建链路：`POST /api/short-links`，成功状态码为 `2xx`。
- 在模拟 2C2G 服务端资源下的稳定吞吐和 p99 延迟。

## 2. 环境

代码目录：

```text
/Users/liangzhancheng/GolandProjects/shortLink
```

服务组成：

- Gin Web 网关：`localhost:8080`
- gRPC RPC 服务：`rpc:50051`
- MySQL 8.0
- Redis 7
- etcd 3.5
- nginx：本次主压测未走 nginx

Docker 环境快照：

```text
CPUs=10
Mem=8217059328
Docker Server=29.2.1
```

对应证据文件：

```text
docs/load-test-artifacts/20260604-141306/docker-info.txt
docs/load-test-artifacts/20260604-141306/docker-compose-ps-before.txt
docs/load-test-artifacts/20260604-141306/docker-images.txt
```

## 3. 压测配置

默认 Web 层有限流：

- API 创建接口：`50 req/s`，burst `100`
- 跳转接口：`500 req/s`，burst `1000`

为了测业务链路能力，本次启动了临时压测版 Web 容器，保持同一镜像和同一后端，只调高限流和 Hystrix 并发：

```bash
docker run -d \
  --name shortlink-web-loadtest \
  --network shortlink_default \
  -p 8080:8080 \
  -e GRPC_ADDR=rpc:50051 \
  -e SHORT_URL_BASE_URL=http://localhost:8080 \
  -e REDIS_ADDR=redis:6379 \
  -e ETCD_ENABLED=true \
  -e ETCD_ENDPOINTS=etcd:2379 \
  -e SERVICE_NAME=short-url-rpc \
  -e RATE_LIMIT_API_RATE=100000 \
  -e RATE_LIMIT_API_BURST=100000 \
  -e RATE_LIMIT_REDIRECT_RATE=100000 \
  -e RATE_LIMIT_REDIRECT_BURST=100000 \
  -e CIRCUIT_BREAKER_MAX_CONCURRENT=10000 \
  -e CIRCUIT_BREAKER_TIMEOUT_MS=5000 \
  shortlink-web
```

本次跳转压测短码：

```text
002GuB
```

对应创建响应：

```text
docs/load-test-artifacts/20260604-141306/seed-create-response.json
```

## 4. 2C2G 模拟方式

通过 Docker cgroup 对服务端容器施加近似 2C2G 限制，压测客户端仍运行在宿主机上，模拟独立压测机。

实际限制如下：

```text
/shortlink-web-loadtest cpuset=0-1 memory=402653184 swap=402653184
/shortlink-rpc-1 cpuset=0-1 memory=402653184 swap=402653184
/shortlink-mysql-1 cpuset=0-1 memory=1073741824 swap=1073741824
/shortlink-redis-1 cpuset=0-1 memory=134217728 swap=134217728
/shortlink-etcd-1 cpuset=0-1 memory=134217728 swap=134217728
```

对应证据文件：

```text
docs/load-test-artifacts/20260604-141306/docker-resource-limits-2c2g.txt
```

## 5. 压测工具

工具：`bombardier`

路径：

```text
/Users/liangzhancheng/go/bin/bombardier
```

版本输出：

```text
bombardier version unspecified darwin/arm64
```

## 6. 压测命令

跳转链路：

```bash
/Users/liangzhancheng/go/bin/bombardier \
  -c 1200 \
  -d 60s \
  -r 12000 \
  -l \
  -o json \
  -p result \
  -t 5s \
  http://localhost:8080/002GuB
```

创建链路：

```bash
/Users/liangzhancheng/go/bin/bombardier \
  -c 800 \
  -d 60s \
  -r 800 \
  -l \
  -o json \
  -p result \
  -t 5s \
  -m POST \
  -H 'Content-Type: application/json' \
  -b '{"origin_url":"https://example.com/create-load-test"}' \
  http://localhost:8080/api/short-links
```

原始结果：

```text
docs/load-test-artifacts/20260604-141306/2c2g-redirect-12000qps-60s.json
docs/load-test-artifacts/20260604-141306/2c2g-create-800qps-60s.json
```

## 7. 结果

| 链路 | 目标压力 | 并发连接 | 时长 | 总请求数 | 成功数 | 错误数 | 实际 QPS | p50 | p95 | p99 | 最大延迟 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 跳转 `GET /002GuB` | 12,000 QPS | 1,200 | 60s | 720,028 | 720,028 `301` | 0 | 12,004.63 | 4.427ms | 9.935ms | 41.013ms | 132.279ms |
| 创建 `POST /api/short-links` | 800 QPS | 800 | 60s | 48,004 | 48,004 `2xx` | 0 | 803.53 | 15.992ms | 29.271ms | 71.425ms | 186.587ms |

结论：

- 在模拟 2C2G 服务端资源下，跳转链路稳定承载约 `12k QPS`，60 秒内无 `4xx/5xx/other`，p99 为 `41.013ms`。
- 在模拟 2C2G 服务端资源下，创建链路稳定承载约 `800 QPS`，60 秒内无 `4xx/5xx/other`，p99 为 `71.425ms`。
- 跳转链路的访问日志异步落库数量与成功跳转请求数一致。

## 8. 验证

压测后健康检查：

```text
healthz: {"status":"ok"}
readyz: {"status":"ready"}
```

对应证据：

```text
docs/load-test-artifacts/20260604-141306/healthz-after.json
docs/load-test-artifacts/20260604-141306/readyz-after.json
```

日志扫描：

```text
docs/load-test-artifacts/20260604-141306/error-log-scan.txt
```

扫描关键词：

```text
error|panic|fatal|failed|timeout|drop|full|queue|失败|丢|满
```

扫描结果：文件为空，表示 Web/RPC 日志尾部没有命中上述异常关键词。

MySQL 访问日志验证：

```text
short_code = 002GuB
short_url_visits count = 720028
```

对应证据：

```text
docs/load-test-artifacts/20260604-141306/mysql-visit-count.txt
```

该计数与跳转压测成功 `301` 请求数 `720,028` 一致。

## 9. 恢复

压测结束后已删除临时 Web 容器，恢复默认 `web` 和 `nginx` 服务。

恢复后 `readyz`：

```text
{"status":"ready"}
```

服务端容器资源限制已放回 Docker VM 可用 CPU `0-9`，内存上限 `7GiB`：

```text
/shortlink-rpc-1 cpuset=0-9 memory=7516192768 swap=7516192768
/shortlink-mysql-1 cpuset=0-9 memory=7516192768 swap=7516192768
/shortlink-redis-1 cpuset=0-9 memory=7516192768 swap=7516192768
/shortlink-etcd-1 cpuset=0-9 memory=7516192768 swap=7516192768
```

对应证据：

```text
docs/load-test-artifacts/20260604-141306/docker-compose-ps-after.txt
docs/load-test-artifacts/20260604-141306/readyz-restored.json
docs/load-test-artifacts/20260604-141306/docker-resource-limits-restored.txt
```

## 10. 简历写法

推荐写法：

```text
基于 Docker Compose 搭建 MySQL、Redis、etcd、gRPC、Gin 网关完整压测环境，并通过 Redis 缓存、singleflight、本地缓存和异步访问日志优化短链跳转链路；在模拟 2C2G 服务端资源下，跳转接口 12k QPS 持续 60 秒无错误，p99 41.013ms，创建接口 800 QPS 持续 60 秒无错误，p99 71.425ms。
```

更保守写法：

```text
完成短链接服务端到端压测和证据归档：在模拟 2C2G 资源下，跳转链路 12k QPS、60 秒 72 万请求 100% 成功、p99 41.013ms；创建链路 800 QPS、60 秒 4.8 万请求 100% 成功、p99 71.425ms。
```

## 11. 局限

- 这是本地 Docker Desktop 压测，不是真实云服务器压测。
- 2C2G 是通过 Docker cgroup 近似模拟，不等同于云厂商真实 2C2G 实例。
- 压测客户端运行在宿主机，没有经过公网网络链路。
- 跳转链路受益于缓存命中和异步访问日志；冷缓存、Redis 故障、MySQL 降级等场景需要单独压测。

