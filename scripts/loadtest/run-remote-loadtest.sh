#!/usr/bin/env bash
# ShortLink 服务器压测编排器（在【压力机】上执行，不在目标服务器上跑）
#
# 它做四件事：
#   1. 前置校验：目标端口可达、/healthz 正常，否则直接退出并给排查提示
#   2. 通过 SSH 在目标服务器上启动 server-observe.sh 采集指标
#   3. 用 k6 按指定模式施压，原始 JSON 摘要落盘
#   4. 停采集、把 CSV 拉回本地，生成 meta.txt 与资源摘要
#
# 用法：
#   TARGET_HOST=1.2.3.4 SSH_HOST=root@1.2.3.4 \
#     bash scripts/loadtest/run-remote-loadtest.sh baseline 2000 60s redirect
#
#   位置参数：MODE RATE DURATION KIND
#     MODE     smoke|baseline|ramp|soak|spike|fixed（默认 smoke）
#     RATE     目标到达率 req/s（默认 1000）
#     DURATION 持续时间（默认 baseline/fixed 60s，soak 30m；ramp/spike 由 stages 决定）
#     KIND     redirect|create|mixed（默认 redirect）
#
# 环境变量：
#   TARGET_HOST  必填，目标服务器公网 IP/域名
#   TARGET_PORT  目标端口，默认 8080
#   SSH_HOST     可选，形如 root@1.2.3.4；给了才能采目标机指标（强烈建议给）
#   SSH_KEY      可选，私钥路径
#   REPO_REMOTE  可选，目标机上的仓库路径，默认 ~/shortLink
#   P99_MS       阈值，默认 500
#   OBSERVE_SECONDS  可选，覆盖目标机采集时长
#   EXTRA_ENV    透传给 k6 的额外环境变量，空格分隔多个，例如：
#                EXTRA_ENV='WRITE_RATIO=0.2 SEED_COUNT=50'
#                EXTRA_ENV='RAMP_STAGES=0:30s,500:1m,1000:2m'
#
# 退出码：0 全部通过；1 前置校验失败；2 压测阈值未达标；3 采集/传输异常

set -uo pipefail

MODE="${1:-smoke}"
RATE="${2:-1000}"
DURATION_RAW="${3:-}"
KIND="${4:-redirect}"

# 未显式给时长时按模式取默认：soak 默认 30m，其余 60s
if [[ -z "$DURATION_RAW" ]]; then
  case "$MODE" in
    soak) DURATION="30m" ;;
    ramp|spike) DURATION="60s" ;;  # 这两个模式的时长由 stages 决定，DURATION 仅作参考
    *) DURATION="60s" ;;
  esac
else
  DURATION="$DURATION_RAW"
fi

TARGET_HOST="${TARGET_HOST:-}"
TARGET_PORT="${TARGET_PORT:-8080}"
SSH_HOST="${SSH_HOST:-}"
SSH_KEY="${SSH_KEY:-}"
P99_MS="${P99_MS:-500}"
EXTRA_ENV="${EXTRA_ENV:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
K6_DIR="$REPO_ROOT/scripts/loadtest/k6"
SCRIPT="$K6_DIR/$KIND.js"
BASE_URL="http://${TARGET_HOST}:${TARGET_PORT}"

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }
die()  { printf '\033[31m[ERROR]\033[0m %s\n' "$1" >&2; exit "${2:-1}"; }

# "60s" / "2m" / "1h" / "500ms" -> 秒
to_seconds() {
  awk -v d="$1" 'BEGIN{
    if (d ~ /ms$/)      { sub(/ms$/,"",d); print int(d/1000) }
    else if (d ~ /s$/)  { sub(/s$/,"",d);  print int(d) }
    else if (d ~ /m$/)  { sub(/m$/,"",d);  print int(d)*60 }
    else if (d ~ /h$/)  { sub(/h$/,"",d);  print int(d)*3600 }
    else                { print int(d) }
  }'
}

[[ -n "$TARGET_HOST" ]] || die "必须设置 TARGET_HOST（目标服务器地址）"
[[ -f "$SCRIPT" ]] || die "找不到压测脚本：$SCRIPT"

# ---------- 1. 前置校验 ----------
say "[1/6] 前置校验 $BASE_URL"
if ! command -v k6 >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then
  die "压力机上既没有 k6 也没有 docker。安装：brew install k6（macOS）/ 见 docs/server-load-test-plan.md"
fi

HEALTH=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$BASE_URL/healthz" || true)
if [[ "$HEALTH" != "200" ]]; then
  printf '\033[33m[WARN]\033[0m /healthz 返回 %s\n' "${HEALTH:-无响应}"
  die "目标不可达。常见原因：
    1) web 容器只绑定了 127.0.0.1:8080 -> 在目标机执行：WEB_BIND=0.0.0.0 docker compose up -d web
    2) 云安全组/防火墙未放行 ${TARGET_PORT}
    3) 服务未启动：docker compose ps 查看健康状态" 1
fi
info "healthz OK"

PING_RTT=$(curl -s -o /dev/null -w '%{time_total}' --max-time 5 "$BASE_URL/healthz" 2>/dev/null || echo "?")
info "控制面往返延迟（网络 floor）：${PING_RTT}s —— 若这个数就已经 > 0.05，说明网络是瓶颈"

# ---------- 2. 证据目录 ----------
TS=$(date +%Y%m%d-%H%M%S)
ARTIFACT_DIR="$REPO_ROOT/docs/load-test-artifacts/${TS}-server-${KIND}-${MODE}-${RATE}qps"
mkdir -p "$ARTIFACT_DIR"
say "[2/6] 证据目录 $ARTIFACT_DIR"

# ---------- 3. 目标机起采集 ----------
OBSERVE_REMOTE=""
if [[ -n "$SSH_HOST" ]]; then
  SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=8)
  [[ -n "$SSH_KEY" ]] && SSH_OPTS+=(-i "$SSH_KEY")
  REMOTE_CSV="/tmp/shortlink-observe-${TS}.csv"
  REMOTE_LOG="/tmp/shortlink-observe-${TS}.log"
  REPO_REMOTE="${REPO_REMOTE:-~/shortLink}"

  info "SSH 连通性检查 $SSH_HOST"
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" 'echo ok' >/dev/null 2>&1 \
    || die "SSH 连接失败：$SSH_HOST（检查密钥/端口/安全组）" 3

  say "[3/6] 在目标机启动指标采集（每 1s 采样）"
  # ramp/spike 的实际时长由 stages 决定，这里按经验值给足采集窗口
  case "$MODE" in
    ramp)  EST_SECONDS=600 ;;
    spike) EST_SECONDS=240 ;;
    *)     EST_SECONDS=$(to_seconds "$DURATION") ;;
  esac
  OBSERVE_SECONDS="${OBSERVE_SECONDS:-$(( EST_SECONDS + 90 ))}"
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" \
    "cd $REPO_REMOTE && rm -f $REMOTE_CSV && nohup env INTERVAL=1 DURATION=$OBSERVE_SECONDS OUT=$REMOTE_CSV bash scripts/loadtest/server-observe.sh >$REMOTE_LOG 2>&1 & echo started" \
    || die "远程启动采集失败（确认目标机 $REPO_REMOTE 下已上传 scripts/loadtest/server-observe.sh）" 3
  OBSERVE_REMOTE="set"
  info "采集已启动，${OBSERVE_SECONDS}s 后自动停止"
else
  say "[3/6] 未设置 SSH_HOST，跳过目标机指标采集（强烈建议补上，否则无法判断是业务慢还是机器满）"
fi

# ---------- 4. 施压 ----------
say "[4/6] 开始施压：kind=$KIND mode=$MODE rate=$RATE duration=$DURATION"
ENV_PAIRS=(
  "BASE_URL=$BASE_URL"
  "MODE=$MODE"
  "RATE=$RATE"
  "DURATION=$DURATION"
  "P99_MS=$P99_MS"
)
if [[ -n "$EXTRA_ENV" ]]; then
  # 对空白分隔的 KEY=VALUE 列表切分（值里可以含逗号，例如 RAMP_STAGES=0:30s,500:1m）
  read -r -a _extra <<<"$EXTRA_ENV"
  for kv in "${_extra[@]}"; do
    [[ -n "$kv" ]] && ENV_PAIRS+=("$kv")
  done
fi

RC=0
if command -v k6 >/dev/null 2>&1; then
  K6_ARGS=()
  for kv in "${ENV_PAIRS[@]}"; do K6_ARGS+=(-e "$kv"); done
  k6 run "${K6_ARGS[@]}" -e "SUMMARY_PATH=$ARTIFACT_DIR/k6-summary.json" "$SCRIPT" \
    2>&1 | tee "$ARTIFACT_DIR/k6-stdout.log" || RC=$?
else
  info "本地无 k6，改用 grafana/k6 容器运行"
  # 注意：macOS 自带 bash 3.2，空数组在 set -u 下展开会报 unbound variable，
  # 所以这里用字符串而不是数组。
  if [[ "$(uname -s)" == "Linux" ]]; then
    DOCKER_NET_OPT="--network host"
    DOCKER_BASE_URL="$BASE_URL"
  else
    DOCKER_NET_OPT=""
    DOCKER_BASE_URL="${BASE_URL//127.0.0.1/host.docker.internal}"
    DOCKER_BASE_URL="${DOCKER_BASE_URL//localhost/host.docker.internal}"
  fi
  if [[ "$DOCKER_BASE_URL" != "$BASE_URL" ]]; then
    info "容器内访问地址改写为 $DOCKER_BASE_URL"
  fi

  DK_ARGS=()
  for kv in "${ENV_PAIRS[@]}"; do
    [[ "$kv" == BASE_URL=* ]] && continue
    DK_ARGS+=(-e "$kv")
  done
  DK_ARGS+=(-e "BASE_URL=$DOCKER_BASE_URL")

  # shellcheck disable=SC2086
  docker run --rm -i $DOCKER_NET_OPT \
    -v "$K6_DIR:/scripts:ro" \
    -v "$ARTIFACT_DIR:/out" \
    "${DK_ARGS[@]}" \
    -e "SUMMARY_PATH=/out/k6-summary.json" \
    grafana/k6 run "/scripts/$KIND.js" \
    2>&1 | tee "$ARTIFACT_DIR/k6-stdout.log" || RC=$?
fi

# 多打了 30s 采集，避免尾部数据缺失
sleep 2

# ---------- 5. 停采集并拉回数据 ----------
if [[ -n "$OBSERVE_REMOTE" ]]; then
  say "[5/6] 停止采集并拉回 CSV"
  # 用 [s] 写法避免 pkill 匹配到自己的命令行
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" "pkill -f '[s]erver-observe.sh' || true" >/dev/null 2>&1 || true
  scp "${SSH_OPTS[@]}" "$SSH_HOST:$REMOTE_CSV" "$ARTIFACT_DIR/observe.csv" >/dev/null 2>&1 \
    && info "已拉回 observe.csv" || info "拉取 CSV 失败（可手工 scp $SSH_HOST:$REMOTE_CSV）"
  ssh "${SSH_OPTS[@]}" "$SSH_HOST" \
    "cat /proc/loadavg; free -m | head -2; docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' 2>/dev/null | head -10" \
    >"$ARTIFACT_DIR/target-snapshot-after.txt" 2>/dev/null || true
else
  say "[5/6] 跳过采集回收"
fi

# ---------- 6. 元信息与资源摘要 ----------
say "[6/6] 生成元信息"
{
  echo "timestamp=$TS"
  echo "kind=$KIND"
  echo "mode=$MODE"
  echo "rate=$RATE"
  echo "duration=$DURATION"
  echo "target=$BASE_URL"
  echo "control_plane_rtt_s=$PING_RTT"
  echo "loadgen_host=$(hostname)"
  echo "loadgen_kernel=$(uname -sr)"
  echo "k6=$(k6 version 2>/dev/null | head -1 || echo docker-grafana-k6)"
  echo "extra_env=$EXTRA_ENV"
} >"$ARTIFACT_DIR/meta.txt"

if [[ -f "$ARTIFACT_DIR/observe.csv" ]]; then
  {
    echo ""
    echo "== 目标机资源峰值（来自 observe.csv） =="
    awk -F, 'NR>2 && NF>=12 {
      if ($4+0 > cpu) { cpu=$4+0 }
      if ($5+0 > mem) { mem=$5+0 }
      if ($8+0 > rx)  { rx=$8+0 }
      if ($9+0 > tx)  { tx=$9+0 }
      if ($10+0 > es) { es=$10+0 }
      if ($1=="01") { }
    } END {
      printf "峰值 CPU 使用率 : %.2f %%\n", cpu
      printf "峰值内存占用   : %.0f MB\n", mem
      printf "峰值入带宽     : %.1f KB/s\n", rx
      printf "峰值出带宽     : %.1f KB/s\n", tx
      printf "峰值 TCP 连接数: %d\n", es
    }' "$ARTIFACT_DIR/observe.csv"
  } >>"$ARTIFACT_DIR/meta.txt"
fi

cat "$ARTIFACT_DIR/meta.txt"

say "完成"
info "原始证据：$ARTIFACT_DIR"
info "k6 判定：k6 阈值未达标时退出码为 99，本次为 $RC"
info "对照方法：把 observe.csv 的 cpu_busy_pct 曲线和 k6-stdout.log 的 p99 放在同一时间轴上——"
info "          延迟飙升的同时 CPU 打满 = 机器瓶颈；CPU 有余量但延迟高 = 下游(MySQL/Redis/锁)瓶颈"

(( RC == 0 )) && exit 0
exit 2
