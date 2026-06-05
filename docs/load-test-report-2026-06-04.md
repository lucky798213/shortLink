# ShortLink Local Load Test Report

Date: 2026-06-04

Status: Superseded by `docs/load-test-report-2026-06-04-formal.md`.

Important note: this draft records the method and numbers observed in the earlier interactive session. A formal rerun with raw artifacts was completed later; use `docs/load-test-report-2026-06-04-formal.md` and `docs/load-test-artifacts/20260604-141306/` as the authoritative source.

## 1. Objective

Measure the end-to-end throughput and tail latency of the short link service under local Docker deployment, especially:

- Redirect path: `GET /:code`, expected success status `301`.
- Create path: `POST /api/short-links`, expected success status `201`.
- Behavior after removing the default Web-layer rate limit as a bottleneck.
- Behavior under an approximate 2-core, 2GiB server resource constraint.

## 2. System Under Test

Repository: `/Users/liangzhancheng/GolandProjects/shortLink`

Services:

- Gin HTTP Web service, exposed on `localhost:8080`.
- gRPC RPC service, exposed internally as `rpc:50051`.
- MySQL 8.0.
- Redis 7.
- etcd 3.5.
- nginx, exposed on `localhost:8888`, not used as the main load-test target.

Main tested URL:

- `http://localhost:8080`

## 3. Baseline Configuration

The default Docker Compose configuration enables Web-layer rate limiting:

- API management rate limit: `50 req/s`, burst `100`.
- Redirect rate limit: `500 req/s`, burst `1000`.

These defaults are suitable for protection testing, but not for measuring backend business-path capacity. For the extreme load test, a temporary Web container was started with higher limits:

```bash
docker compose stop nginx web

docker rm -f shortlink-web-loadtest

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

This keeps RPC, MySQL, Redis, and etcd unchanged, while preventing the default limiter from turning the benchmark into a `429` test.

## 4. Tooling

Main load-test tool:

```bash
go install github.com/codesenberg/bombardier@latest
```

Example command shape:

```bash
/Users/liangzhancheng/go/bin/bombardier \
  -c 1200 \
  -d 60s \
  -r 12000 \
  -l \
  -o json \
  -p result \
  -t 5s \
  http://localhost:8080/000Yct
```

For redirects, `bombardier` was used without following the target URL. `301` was counted as success.

For create requests:

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
  -b '{"origin_url":"https://example.com/create-extreme-static"}' \
  http://localhost:8080/api/short-links
```

The create endpoint generates a new short code for every request, so using the same `origin_url` does not collapse requests into one record.

## 5. 2C2G Simulation Method

The 2-core, 2GiB scenario was approximated with Docker cgroup limits. The load generator still ran on the host machine, simulating a separate client machine.

Applied limits:

```bash
docker update --cpuset-cpus 0-1 --memory 384m --memory-swap 384m shortlink-web-loadtest
docker update --cpuset-cpus 0-1 --memory 384m --memory-swap 384m shortlink-rpc-1
docker update --cpuset-cpus 0-1 --memory 1024m --memory-swap 1024m shortlink-mysql-1
docker update --cpuset-cpus 0-1 --memory 128m --memory-swap 128m shortlink-redis-1
docker update --cpuset-cpus 0-1 --memory 128m --memory-swap 128m shortlink-etcd-1
docker restart shortlink-web-loadtest
```

Observed effective resource allocation:

```text
shortlink-web-loadtest cpuset=0-1 memory=384MiB
shortlink-rpc-1        cpuset=0-1 memory=384MiB
shortlink-mysql-1      cpuset=0-1 memory=1GiB
shortlink-redis-1      cpuset=0-1 memory=128MiB
shortlink-etcd-1       cpuset=0-1 memory=128MiB
```

## 6. Observed Results

### 6.1 Default Rate Limit Verification

| Scenario | Target Load | Total | Success | Limited | Effective Success QPS | Success p95 | Success p99 |
|---|---:|---:|---:|---:|---:|---:|---:|
| Create `POST /api/short-links` | 50 req/s, 20s | 1,000 | 1,000 `201` | 0 | 50.02 | 14.82ms | 15.62ms |
| Create `POST /api/short-links` | 150 req/s, 20s | 3,000 | 1,099 `201` | 1,901 `429` | 54.95 | 11.50ms | 15.19ms |
| Redirect `GET /0000g9` | 500 req/s, 20s | 10,000 | 10,000 `301` | 0 | 500.04 | 2.14ms | 3.43ms |
| Redirect `GET /0000g9` | 1,000 req/s, 20s | 20,000 | 11,036 `301` | 8,964 `429` | 551.79 | 1.68ms | 6.30ms |

Conclusion: default rate limiting works as configured. These are not backend capacity numbers.

### 6.2 Unrestricted Local Docker VM Results

Docker Desktop environment observed during the run:

```text
CPUs=10
Memory about 7.65GiB available to Docker VM
```

| Scenario | Target Load | Duration | Total | Success Rate | Actual QPS | p95 | p99 | Max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Redirect | 20,000 req/s | 20s | 400,070 | 100% | 20,001.65 | 11.50ms | 26.40ms | 116.64ms |
| Create | 2,000 req/s | 20s | 40,020 | 100% | 1,995.55 | 111.90ms | 128.96ms | 149.28ms |
| Create | 2,500 req/s | 20s | 50,004 | 100% | 2,399.35 | 847.96ms | 881.93ms | 908.53ms |
| Create | 3,000 req/s | 20s | 52,264 | 100% | 2,479.04 | 1,224.15ms | 1,245.45ms | 1,269.52ms |

Conclusion: redirect path is very cache-friendly and reaches high throughput. Create path begins to queue heavily above about 2,000 req/s in this local environment.

### 6.3 2C2G Approximation Results

#### 20-second exploration

| Scenario | Target Load | Duration | Total | Success Rate | Actual QPS | p95 | p99 | Max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Redirect | 8,000 req/s | 20s | 160,080 | 100% | 8,005.83 | 5.34ms | 22.56ms | 138.68ms |
| Redirect | 12,000 req/s | 20s | 240,111 | 100% | 12,006.44 | 7.92ms | 45.10ms | 115.91ms |
| Redirect | 16,000 req/s | 20s | 303,943 | 100% | 15,123.41 | 121.18ms | 139.31ms | 196.48ms |
| Redirect | 20,000 req/s | 20s | 286,652 | 100% | 14,241.21 | 155.80ms | 165.30ms | 288.18ms |
| Create | 500 req/s | 20s | 10,005 | 100% | 500.06 | 9.89ms | 13.35ms | 51.77ms |
| Create | 1,000 req/s | 20s | 20,010 | 100% | 999.38 | 31.10ms | 45.83ms | 65.77ms |
| Create | 2,000 req/s | 20s | 40,020 | 100% | 1,991.07 | 110.45ms | 143.56ms | 178.92ms |
| Create | 2,500 req/s | 20s | 50,019 | 100% | 2,469.31 | 387.01ms | 419.17ms | 443.04ms |

#### 60-second validation

| Scenario | Target Load | Duration | Total | Success Rate | Actual QPS | p50 | p95 | p99 | Max |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| Redirect | 12,000 req/s | 60s | 720,010 | 100% | 11,999.52 | 4.70ms | 11.37ms | 44.31ms | 125.34ms |
| Create | 800 req/s | 60s | 48,008 | 100% | 800.28 | 8.73ms | 22.67ms | 73.26ms | 260.51ms |
| Create | 1,000 req/s | 60s | 60,000 | 100% | 999.67 | 18.36ms | 70.31ms | 440.20ms | 569.92ms |
| Create | 2,000 req/s | 60s | 120,020 | 100% | 2,004.96 | 83.78ms | 365.74ms | 714.38ms | 819.26ms |

Conclusion: in the 2C2G approximation, redirect remains stable at 12,000 QPS with p99 about 44ms. Create remains stable at 800 QPS with p99 about 73ms. Higher create rates still succeed, but p99 latency grows sharply, so they are not good resume headline numbers.

## 7. Verification

After the 2C2G run:

- `GET /healthz` returned `{"status":"ok"}`.
- `GET /readyz` returned `{"status":"ready"}`.
- Web and RPC logs were checked for:
  - `error`
  - `panic`
  - `fatal`
  - `failed`
  - `timeout`
  - `queue full`
  - `dropping visit`
- No matching Web/RPC error logs were observed in the checked tail.
- For the 2C2G redirect short code `000Yct`, MySQL visit count was observed as `1,850,852`, matching the sum of successful redirect requests in that stage.

## 8. Recommended Resume Wording

Preferred:

```text
基于 Docker Compose 搭建 MySQL、Redis、etcd、gRPC、Gin 网关完整压测环境，并通过 Redis 缓存、singleflight、本地缓存和异步访问日志优化短链跳转链路；在模拟 2C2G 服务器资源下，跳转接口稳定承载 12k QPS，p99 44.31ms，创建接口稳定承载 800 QPS，p99 73.26ms。
```

More conservative:

```text
完成短链接服务端到端压测和限流验证：在模拟 2C2G 资源下，跳转链路 12k QPS、60 秒 72 万请求 100% 成功、p99 44.31ms；创建链路 800 QPS、60 秒 4.8 万请求 100% 成功、p99 73.26ms。
```

Avoid:

```text
系统支持 20k QPS。
```

Reason: 20k QPS was observed on an unrestricted local Docker VM and should not be presented without the environment qualifier.

## 9. Limitations

- This was a local Docker Desktop test, not a real cloud server test.
- The 2C2G setup is an approximation using container cgroup limits.
- The load generator ran on the host machine, not over a real external network.
- The previous run did not save raw JSON files during execution. Re-run with `docs/load-test-runbook.md` before treating the p99 numbers as formal evidence.
- Redirect performance benefits from cache hits and asynchronous visit logging. Cold-cache and database-fallback scenarios should be tested separately if needed.
