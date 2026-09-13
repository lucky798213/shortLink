# ShortLink 服务器压测方案

面向「目标服务跑在服务器上、压力源在另一台机器上」的压测形态。
本地单机版流程见 `docs/load-test-runbook.md`，两者互补：本文档解决的是**数据可信度**问题。

## 0. 为什么不能直接用本地 runbook

本地 runbook 把 `bombardier` 和服务跑在同一台机器上，测的是「服务 + 压测进程 + Docker 一起抢 CPU」的结果。

| 维度 | 本地 runbook（单机） | 本文方案（服务器 + 独立压力机） |
| --- | --- | --- |
| 压力源 | 同机 bombardier | 另一台机器上的 k6 |
| 数据可信度 | 低，CPU 被压测进程吃掉 | 高，目标机资源全部给服务 |
| 施压模型 | 闭模型（连接数固定） | **开模型（恒定到达率）**，避免 coordinated omission |
| 场景覆盖 | 单一恒定压力 | 冒烟/基准/阶梯/稳定性/尖峰 + 读写混合 |
| 观测 | docker stats 快照 | 1s 粒度 CSV 时序（CPU/内存/带宽/TCP/Redis/MySQL） |
| 适用 | 功能冒烟、回归 | 容量评估、瓶颈定位、写进简历的数字 |

一句话：**要出能写进简历的性能数字，压力机必须独立。**

## 1. 拓扑

```text
┌──────────────────────┐            ┌───────────────────────────────────────────┐
│  压力机 (Load Gen)    │            │  目标服务器 (2C2G)                          │
│                      │  公网/内网  │                                           │
│  k6 run redirect.js  │ ─────────► │  web 容器 (:8080)   ← Gin，限流+熔断        │
│  (开模型恒定到达率)    │  HTTP :8080│    │                                      │
│                      │            │    ▼ gRPC :50051                          │
│                      │  SSH       │  rpc 容器           ← 号段ID/多级缓存/布隆   │
│  run-remote-loadtest │ ◄────────► │    │        │        │                    │
│  server-observe.sh ──┼─ 采集拉回 ─┤    ▼        ▼        ▼                    │
│  (在目标机 1s 采样)   │            │  MySQL    Redis     etcd                  │
└──────────────────────┘            └───────────────────────────────────────────┘
```

压力机可以是：另一台云服务器、你的 Mac（外网打服务器，注意出口带宽与公网延迟）、
或云厂商压测服务（见 §5）。

## 2. 落地流程

### Step 0 · 目标机体检

把仓库同步到目标机后执行（只读，不改任何系统参数）：

```bash
bash scripts/loadtest/server-check.sh
```

它会告诉你四件事：机器规格、文件描述符/内核网络参数是否够用、**端口是否只绑了 127.0.0.1**、
MySQL/Redis 的连接上限。其中前两类问题必须先解决：

| 检查项 | 典型问题 | 处理 |
| --- | --- | --- |
| 端口绑定 | `web` 只绑 `127.0.0.1:8080`，外部打不进来 | 见 Step 1 |
| Web 层限流 | 默认创建 50/s、跳转 500/s | 见 Step 1 |
| nginx 限流 | `limit_req 100r/s burst 200`（按来源 IP） | 压测别走 nginx，或改用无限流 conf |
| ulimit -n | < 65535 时高并发会 `too many open files` | `ulimit -n 65535` |
| 内核参数 | `somaxconn` / `tcp_tw_reuse` 偏小 | 见 server-check.sh 输出里的建议值 |
| 安全组 | 未放行压测端口 | 控制台临时放行，**测完立刻关闭** |

### Step 1 · 目标机准备（把限流放开）

`docker-compose.yaml` 已支持用环境变量覆盖绑定地址与限流参数，默认值不变（仍然只绑本机、仍然限流）。
压测时一条命令重建 web 容器即可。

> 先确认仓库在服务器上的**真实路径**（不要凭记忆写 `~/shortLink`，clone 位置因人而异）。
> 最可靠的办法是问 Docker 自己，compose 会把工作目录记在容器 label 上：
>
> ```bash
> docker inspect shortlink-web-1 --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}'
> ```
>
> 输出的路径就是下面 `cd` 的目标（容器名 `shortlink-web-1` 里的 `shortlink` 是 compose 项目名）。
> 需要 `cd` 到该目录，`docker compose` 才能读到同一份 `docker-compose.yaml`。

```bash
cd ~/shortLink
WEB_BIND=0.0.0.0 \
RATE_LIMIT_API_RATE=100000 RATE_LIMIT_API_BURST=100000 \
RATE_LIMIT_REDIRECT_RATE=100000 RATE_LIMIT_REDIRECT_BURST=100000 \
CIRCUIT_BREAKER_MAX_CONCURRENT=10000 CIRCUIT_BREAKER_TIMEOUT_MS=5000 \
docker compose up -d web

docker compose ps          # 确认 web 是 healthy
```

> 这里只放开**应用层**限流，不放开 nginx 的 `limit_req`。压测目标端口是 `8080`，
> 不经过 nginx，避免把 nginx 的 100r/s 误当成业务瓶颈。
> 如果想顺带压 nginx，改 `nginx/nginx.conf` 里的 `rate=100r/s` 后重建 nginx。

### Step 2 · 压力机准备

```bash
# macOS
brew install k6
# Linux（Debian/Ubuntu）
sudo gpg -k && sudo apt-get install -y gnupg ca-certificates
# 或直接下二进制
curl -fsSL https://github.com/grafana/k6/releases/latest/download/k6-linux-amd64.tar.gz | tar xz
```

没有 k6 时，编排脚本会自动退回 `grafana/k6` 容器（Linux 上加 `--network host`）。

### Step 3 · 执行

一条命令跑完整套：校验连通性 → 远程起采集 → 施压 → 拉回指标 → 生成证据目录。

```bash
cd /Users/liangzhancheng/GolandProjects/shortLink

# 冒烟：先确认链路通（2 VU × 10s）
TARGET_HOST=<服务器IP> SSH_HOST=root@<服务器IP> \
  bash scripts/loadtest/run-remote-loadtest.sh smoke

# 基准：2000 req/s 打 60s
TARGET_HOST=<服务器IP> SSH_HOST=root@<服务器IP> \
  bash scripts/loadtest/run-remote-loadtest.sh baseline 2000 60s redirect

# 阶梯：找拐点（默认 0→500→1000→2000→3000，约 8 分钟）
TARGET_HOST=<服务器IP> SSH_HOST=root@<服务器IP> \
  bash scripts/loadtest/run-remote-loadtest.sh ramp

# 读写混合 9:1
TARGET_HOST=<服务器IP> SSH_HOST=root@<服务器IP> EXTRA_ENV='WRITE_RATIO=0.1' \
  bash scripts/loadtest/run-remote-loadtest.sh baseline 2000 120s mixed

# 自定义阶梯（EXTRA_ENV 内多个变量用空格分隔，值里的逗号是阶梯分隔符）
TARGET_HOST=<服务器IP> SSH_HOST=root@<服务器IP> \
  EXTRA_ENV='RAMP_STAGES=0:30s,500:1m,1000:2m,2000:2m' \
  bash scripts/loadtest/run-remote-loadtest.sh ramp
```

> 目标机 2C2G 的 `RATE` 建议从 500 起步，按阶梯结果逐步上调，不要一上来就 3000。

想不用编排脚本、直接手跑 k6（例如已经在内网压力机上）：

```bash
MODE=baseline RATE=2000 DURATION=60s \
  k6 run -e SUMMARY_PATH=./out.json scripts/loadtest/k6/redirect.js
```

k6 脚本本身不依赖任何外网资源（不用 jslib），单个二进制可直接跑。

产物落在 `docs/load-test-artifacts/<时间戳>-server-<类型>-<模式>-<速率>qps/`：

```text
k6-stdout.log          # 终端摘要（QPS / p50 / p90 / p99 / 错误率 / dropped_iterations）
k6-summary.json        # 原始 JSON，可二次分析画图
observe.csv            # 目标机 1s 粒度时序指标
target-snapshot-after.txt
meta.txt               # 环境、参数、网络 floor、资源峰值
```

### Step 4 · 读结果

判断顺序（很重要，别一上来就怀疑代码）：

1. **网络 floor**：`meta.txt` 里的 `control_plane_rtt_s`。如果这个数就已经几十毫秒，说明公网是瓶颈，压出来的延迟没意义 —— 换内网压力机。
2. **压力机自己有没有打满**：`k6-stdout.log` 里 `dropped_iterations > 0` 说明预分配 VU 不够，实际到达率没打满（把 `VUS` 调大重跑）；同时看压力机自身 CPU。
3. **目标机 CPU 曲线**（`observe.csv` 的 `cpu_busy_pct`）：
   - 延迟飙升 **且** CPU 打满 → 机器瓶颈，看是 web 还是 rpc 吃满（`docker_cpu_pct`）
   - CPU 有余量 **但**延迟高 → 下游瓶颈：看 `mysql_running`、`redis_ops`、`tcp_tw`
   - CPU 不高、延迟也不高但 QPS 上不去 → 压力机或网络瓶颈
4. **错误类型**：k6 摘要里 4xx/5xx 比例。跳转链路的成功码是 **301**（k6 默认不跟随重定向，200–399 都算 expected）。

## 3. 场景矩阵

| 模式 | 命令参数 | 目的 | 关键观察 |
| --- | --- | --- | --- |
| 冒烟 | `smoke` | 链路是否通、种子数据是否可用 | 301 比例、`created_total` |
| 基准 | `baseline RATE DURATION` | 稳态吞吐与 p99 | p99、CPU 水位 |
| 阶梯 | `ramp` | 找性能拐点与饱和点 | 拐点出现在多少 QPS、之后是否雪崩 |
| 稳定性 | `soak RATE=1000`（默认 30m） | 内存泄漏、连接泄漏、慢 SQL 累积 | `mem_used_mb` 是否单调上升、`tcp_tw` 是否堆积 |
| 尖峰 | `spike RATE=3000` | 削峰能力与恢复速度 | 突增期间错误率、回落段能否自愈 |
| 固定并发 | `fixed VUS=200` | 与 wrk 等闭模型工具对比 | 仅用于横向对比，不作为容量结论 |

## 4. 观测指标口径

`observe.csv` 列（1s 一行）：

```text
ts, load1, load5, cpu_busy_pct, mem_used_mb, mem_avail_mb, swap_used_mb,
net_rx_kbps, net_tx_kbps, tcp_estab, tcp_tw,
docker_cpu_pct, docker_mem_mb, redis_ops, redis_clients, mysql_running, mysql_connected
```

阈值判据（个人项目/校招场景的合理档位，可按需调整）：

| 指标 | 健康 | 需要注意 |
| --- | --- | --- |
| `http_req_failed` | < 1% | > 1% 说明有限流误伤、超时或下游报错 |
| 跳转 p99 | < 200 ms | > 500 ms 要定位是排队还是回源 |
| 创建 p99 | < 500 ms | 写路径涉及 MySQL 批量落库，天然更高 |
| `cpu_busy_pct` | < 70% | > 85% 说明已饱和，QPS 数据不代表可服务容量 |
| `swap_used_mb` | 0 | > 0 说明内存不够，数据不可信 |
| `tcp_tw` | 稳定 | 持续单调上升 = 短连接回收有问题 |

## 5. 工具选型

| 工具 | 类型 | 开模型 | 适用场景 | 备注 |
| --- | --- | --- | --- | --- |
| **k6** | Go/JS 脚本 | ✅ 恒定/阶梯到达率 | 首选。场景丰富、阈值断言、CI 友好 | 单个二进制，本方案默认 |
| wrk / wrk2 | C | wrk2 支持 | 极限吞吐快速摸底 | wrk 有 coordinated omission，数字偏乐观 |
| bombardier | Go | 部分 | 本地 runbook 已用 | 简单直接，场景能力弱 |
| vegeta | Go | ✅ 恒定速率 | 命令行快速压测、结果可画图 | 无复杂场景编排 |
| Locust | Python | ✅ | 需要**多机分布式**施压 | master/worker 架构，压测机集群 |
| JMeter | Java | 闭模型为主 | 复杂业务编排、团队已有资产 | 资源开销大，单机量级低 |
| 云压测（阿里云 PTS / 腾讯云压测） | SaaS | ✅ | 无自备压力机、需要多地域 IP、一次性验证 | 按量付费，自带报表 |

什么时候需要分布式压测：目标机规格上去了（比如 8C16G）或者需要 > 100k QPS，
单台压力机先到瓶颈时，用 Locust master/worker 或多台 k6 同时施压再聚合结果。

## 6. 常见坑

1. **压测进程跑在目标机上** —— 最致命。CPU 一被抢，所有数字作废。
2. **忘了放开限流** —— 压出来恒定 50 qps / 500 qps 的平线，就是限流器在工作。
3. **走 nginx 压测却没改 `limit_req`** —— 被限在 100 r/s，还会误判成「服务只能扛 100」。
4. **闭模型看延迟** —— 用固定线程数压，服务变慢时线程被拖住、请求数下降，你会看到一个「延迟还行」的假象。
   这就是 coordinated omission，用到达率模型规避。
5. **不预热就出数** —— 多级缓存（Local→Redis→Bloom）冷启动那几秒全是 MySQL 回源，p99 会被头几秒拉爆。先跑 10–20s 预热再计时。
6. **只看平均值** —— p50 好看不代表能用，容量结论一律看 p99。
7. **写压测污染数据** —— `create` 场景会真实写入 MySQL。数据落在 **64 张分片表** `short_urls_00`~`short_urls_63`，
   跳转统计落在 `short_url_visits`，跑完要清理（见 §7）。
8. **测完不恢复** —— 忘关安全组端口、忘把 `WEB_BIND` 改回 `127.0.0.1`、忘恢复限流。见 §7。
9. **压测机自己到瓶颈** —— 压力机 CPU 打满 / 出口带宽打满 / 本地端口耗尽（`can't assign requested address`），
   表现为 QPS 上不去但目标机很闲。
10. **公网带宽陷阱** —— 压测数据有响应体时，公网小带宽（1–5 Mbps）会先成为瓶颈。跳转链路返回的是空 body 的 301，影响不大；
    若压测目标返回大 body，可在脚本 options 里加 `discardResponseBodies: true`（注意它是全局的，会让 `setup()` 也拿不到响应体），并尽量走内网。
11. **压力机上开着代理** —— k6 会读取 `HTTP_PROXY`/`HTTPS_PROXY`，Clash/Surge 一开，请求全被本地代理接管，
    压出来的是代理进程的吞吐。施压前 `unset` 掉，或先跑 `scripts/loadtest/loadgen-check.sh`。
12. **把限流器的输出当成容量** —— 典型症状：**不同并发下「成功请求数」几乎恒定**（例如 wrk 各档都恰好成功 ~3200 次/30s
    ≈ 100 r/s × 30s + burst 200）。看到这种「平线」先查限流，不要去找代码的锅。
13. **命令前加的环境变量根本没进容器** —— `RATE_LIMIT_REDIRECT_RATE=100000 docker compose up -d web`
    只在 compose 文件里写了 `${RATE_LIMIT_REDIRECT_RATE:-500}` 这类占位符时才生效。
    如果目标机上那份 compose 是旧版（没有占位符段），环境变量会被**静默丢弃**，容器里仍是默认的 500 r/s + burst 1000。
    典型症状：成功速率被压在 **~500 req/s** 附近，且返回 **429**（应用层令牌桶；nginx 的 limit_req 返回的是 503）。
    核对容器真正拿到的参数：

    ```bash
    docker inspect shortlink-web-1 --format '{{range .Config.Env}}{{println .}}{{end}}' | grep -E 'RATE_LIMIT|CIRCUIT'
    ```

    输出为空、或仍是 500 / 1000 → 必须先把新版 `docker-compose.yaml` 同步到目标机（`git pull` 或 scp）再重建容器。
14. **自检脚本用 curl 做限流探测会漏判** —— `curl` 每请求新起进程 + 新建连接，实际速率只有一两百 req/s，
    压不到 500/1000 的阈值，于是报告「没有限流」。`loadgen-check.sh` 已改为优先用 `wrk`（连接复用、C 实现）
    拿持续速率，并打印「成功被压在约 N req/s」这个关键数字；若该数明显低于你配置的限流值，就是限流在起作用。

## 7. 收尾清单

```bash
# 1. 恢复 web 端口绑定与限流（不带环境变量重建即可回到 compose 默认值）
docker compose up -d web nginx

# 2. 清理压测写入的数据（A：精确清理，保留其他数据）
#    压测数据的 origin_url 前缀为 https://example.com/lt，数据分布在 64 张分片表
docker compose exec -T mysql mysql -uroot -proot -N -e "
  SELECT CONCAT('DELETE FROM short_url.', table_name,
                ' WHERE origin_url LIKE ''https://example.com/lt%'';')
  FROM information_schema.tables
  WHERE table_schema='short_url' AND table_name LIKE 'short_urls\\_%';" \
| docker compose exec -T mysql mysql -uroot -proot

# 2'. 清理跳转统计（B：只想看数据干净的库就直接重置）
docker compose exec -T mysql mysql -uroot -proot -e "TRUNCATE TABLE short_url.short_url_visits;"

# 3. 关掉云安全组临时放行的端口

# 4. 归档证据
ls docs/load-test-artifacts/ | tail -1
```

## 8. 顺带可写进面试的点

这套流程本身就是可讲的工程能力，面试时按「怎么测 → 怎么判 → 怎么归因」三段讲：

- **为什么用开模型（恒定到达率）而不是固定并发**：固定并发是闭模型，服务变慢时压不进去，
  延迟统计会被系统性低估（coordinated omission）。k6 的 `constant-arrival-rate` 是开模型，
  真实反映「请求按时到达」的场景。
- **怎么区分「机器瓶颈」和「下游瓶颈」**：把目标机 1s 粒度的 CPU 曲线和 k6 的延迟时间轴对齐 ——
  延迟飙升同时 CPU 打满 = 算力瓶颈；CPU 有余量但延迟高 = 下游（MySQL/Redis/锁/连接池）瓶颈。
- **2C2G 上的容量结论**：在明确限流放开、缓存预热、资源受限的前提下，能给出
  「跳转链路 p99 < X ms 时可稳定支撑 Y QPS」这种带条件的结论，比裸报一个 QPS 数字专业得多。
- **压测暴露的真实问题**：例如 `tcp_tw` 堆积说明短连接没收复用（HTTP keep-alive 没开或客户端没复用连接），
  `mysql_running` 尖刺说明有慢查询或锁竞争 —— 这些是能带出优化动作的发现，面试官更想看这个。

## 附录：文件清单

```text
docs/server-load-test-plan.md                 本文档
scripts/loadtest/loadgen-check.sh             压力机前置自检（代理/限流/算力/网络 floor）
scripts/loadtest/server-check.sh              目标机前置体检（只读）
scripts/loadtest/server-observe.sh            目标机 1s 粒度指标采集 → observe.csv
scripts/loadtest/run-remote-loadtest.sh       压力机侧一键编排（SSH 起采集 → 施压 → 拉回）
scripts/loadtest/k6/lib/common.js             场景构造 / 摘要输出（不依赖外网）
scripts/loadtest/k6/redirect.js               跳转链路（301）
scripts/loadtest/k6/create.js                 创建链路（2xx + short_code）
scripts/loadtest/k6/mixed.js                  读写混合（默认 9:1，按 tag 分别设阈值）
```

本地已验证（用 mock 服务跑通 `redirect` / `create` / `mixed` 与 `baseline` / `ramp` 两种模式，
未在真实服务器上执行）；`server-observe.sh` 依赖 Linux `/proc`，需在目标服务器上验证。

### 环境变量速查

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `BASE_URL` | 目标地址 | `http://localhost:8080` |
| `MODE` | `smoke`/`baseline`/`ramp`/`soak`/`spike`/`fixed` | `smoke` |
| `RATE` | 目标到达率 req/s | 1000 |
| `DURATION` | 持续时间 | `60s`（soak 为 `30m`） |
| `VUS` | 预分配 VU，留空按 `RATE×0.5` 估算 | 自动 |
| `CODES` | 逗号分隔的短码列表，留空则自动造 `SEED_COUNT` 条 | 自动造 20 条 |
| `P99_MS` | p99 阈值，超了 k6 退出码 99 | 500（create 800） |
| `WRITE_RATIO` | `mixed.js` 写占比 | 0.1 |
| `RAMP_STAGES` | 阶梯序列 `目标:时长,...` | `0:30s,500:1m,1000:2m,2000:2m,3000:3m` |
| `SUMMARY_PATH` | 原始 JSON 摘要输出路径 | 不输出 |
| `WEB_BIND` | 目标机 compose 的 web 绑定地址 | `127.0.0.1` |
