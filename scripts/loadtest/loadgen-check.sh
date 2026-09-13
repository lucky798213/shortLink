#!/usr/bin/env bash
# loadgen-check.sh —— 【压力机】侧前置自检（macOS / Linux 通用）
#
# 为什么需要它：Mac 当压力机时，最常见的翻车不是服务器慢，而是压力机这边
#   (1) 系统代理把请求劫持到本地代理进程 —— 你压的是代理，不是服务器；
#   (2) 目标前面挂着限流器（nginx limit_req / 应用层令牌桶）—— 你压的是限流器；
#   (3) 文件描述符或临时端口不够 —— 压到一半 `can't assign requested address`。
# 这三件事都会产出「看起来很正常」的假数字。本脚本把它们在施压前一次性暴露出来。
#
# 用法：
#   bash scripts/loadtest/loadgen-check.sh http://1.2.3.4:8080/000001
#   bash scripts/loadtest/loadgen-check.sh http://1.2.3.4:8080/000001 600   # 第二参数=限流探测请求数
#
# 退出码：0 可以开压；1 必须先修（代理/限流/不可达）

set -uo pipefail

TARGET_URL="${1:-}"
PROBE_N="${2:-400}"
PROBE_C="${PROBE_C:-40}"   # 并发度

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✅ %s\033[0m\n' "$*"; }
warn() { printf '  \033[33m⚠️  %s\033[0m\n' "$*"; }
bad()  { printf '  \033[31m❌ %s\033[0m\n' "$*"; }
info() { printf '  ·  %s\n' "$*"; }

[[ -n "$TARGET_URL" ]] || { echo "用法: bash $0 <目标URL，含具体路径> [限流探测请求数]" >&2; exit 1; }

EXIT=0
OS="$(uname -s)"

echo
say "═══ 压力机自检 $(hostname) · $(uname -sr) ═══"
echo

# ---------- 1. 本机算力 ----------
say "[1/5] 本机算力"
if [[ "$OS" == "Darwin" ]]; then
  CORES=$(sysctl -n hw.ncpu 2>/dev/null || echo "?")
  MEM_GB=$(sysctl -n hw.memsize 2>/dev/null | awk '{printf "%.0f", $1/1073741824}')
else
  CORES=$(nproc 2>/dev/null || echo "?")
  MEM_GB=$(awk '/MemTotal/{printf "%.0f", $2/1048576}' /proc/meminfo 2>/dev/null || echo "?")
fi
info "CPU 核心 ${CORES} / 内存 ${MEM_GB} GB"
# 经验值：单核 k6 约能产生 8k~15k req/s（含 TLS 时更低）；wrk 更高但只有闭模型
EST_LOW=$(( CORES * 8000 )); EST_HIGH=$(( CORES * 15000 ))
info "粗估本机施压上限约 ${EST_LOW}~${EST_HIGH} req/s，目标低于此值才谈得上「测服务器」"
echo

# ---------- 2. 代理环境（Mac 压测第一大坑） ----------
say "[2/5] 代理环境检查"
PROXY_ENV_HIT=0
for v in HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy; do
  val="${!v:-}"
  if [[ -n "$val" ]]; then
    bad "环境变量 $v=$val —— k6 会读取它，请求会打到本地代理进程，结果失真"
    PROXY_ENV_HIT=1
  fi
done

# 系统代理（网络设置里的那个）只在「代理环境变量已清除」时提示，不算失败：
# k6 是 Go 程序，只认 HTTP_PROXY / HTTPS_PROXY / NO_PROXY 环境变量，不读 macOS 系统代理设置。
SYS_PROXY=0
if [[ "$OS" == "Darwin" ]]; then
  if scutil --proxy 2>/dev/null | grep -qE '(HTTPEnable|HTTPSEnable|SOCKSEnable) : 1'; then
    SYS_PROXY=1
  fi
fi

if (( PROXY_ENV_HIT == 1 )); then
  warn "清除方式：unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy"
  warn "（wrk 走原生 socket 不受影响；curl 临时绕过加 curl --noproxy '*'）"
  EXIT=1
elif (( SYS_PROXY == 1 )); then
  ok "代理环境变量已清除，不影响 k6"
  warn "系统代理仍开启（设置→网络→代理），但 k6 不读系统代理设置，可以忽略"
  info "结果异常时再考虑临时关掉 Clash/Surge；查看详情: scutil --proxy"
else
  ok "未发现代理配置"
fi
echo

# ---------- 3. 文件描述符与临时端口 ----------
say "[3/5] 文件描述符 / 临时端口"
NOFILE="$(ulimit -n 2>/dev/null)"
[[ "$NOFILE" =~ ^[0-9]+$ ]] || NOFILE=""
if [[ -z "$NOFILE" ]]; then
  warn "当前 shell 读不到 ulimit（zsh 的 rlimits 模块缺失时会这样），本项跳过"
  info "手动确认：ulimit -n"
elif (( NOFILE < 10240 )); then
  bad "ulimit -n = $NOFILE，太低，高并发会 too many open files"
  info "执行：ulimit -n 65535   （只对当前终端生效，压测完可关）"
  EXIT=1
else
  ok "ulimit -n = $NOFILE"
fi

if [[ "$OS" == "Darwin" ]]; then
  FIRST=$(sysctl -n net.inet.ip.portrange.first 2>/dev/null)
  LAST=$(sysctl -n net.inet.ip.portrange.last 2>/dev/null)
  MAXPROC=$(sysctl -n kern.maxfilesperproc 2>/dev/null)
  if [[ "$FIRST" =~ ^[0-9]+$ && "$LAST" =~ ^[0-9]+$ ]]; then
    info "临时端口范围 ${FIRST}-${LAST}（约 $(( LAST - FIRST + 1 )) 个）/ kern.maxfilesperproc=${MAXPROC}"
  else
    info "临时端口范围读取失败 / kern.maxfilesperproc=${MAXPROC}"
  fi
  TW=$(netstat -an -p tcp 2>/dev/null | grep -c "TIME_WAIT")
  TW=${TW:-0}
  [[ "$TW" =~ ^[0-9]+$ ]] || TW=0
  info "当前 TIME_WAIT 连接数：${TW}"
  if (( TW > 5000 )); then
    warn "TIME_WAIT 偏多，等 60s 自然回收，否则新连接可能分配不到端口"
  fi
else
  info "本地端口范围: $(cat /proc/sys/net/ipv4/ip_local_port_range 2>/dev/null || echo '?')"
  info "TIME_WAIT: $(ss -tan 2>/dev/null | grep -c TIME-WAIT || echo '?')"
fi
info "提示：wrk / k6 默认复用连接（keep-alive），端口不是瓶颈；只有用短连接压才会耗尽端口"
echo

# ---------- 4. 目标可达性 + 网络 floor ----------
say "[4/5] 目标可达性与网络 floor"
curl -sS --noproxy '*' --max-time 8 -o /dev/null -w '' "$TARGET_URL" 2>/dev/null \
  || { bad "目标不可达：$TARGET_URL"; info "排查：web 容器是否只绑 127.0.0.1？安全组是否放行端口？"; exit 1; }

RTT_SUM=0; RTT_MIN=999999; N_OK=0
for i in 1 2 3 4 5; do
  t=$(curl -s --noproxy '*' --max-time 8 -o /dev/null -w '%{time_total}' "$TARGET_URL" 2>/dev/null || echo "")
  [[ -z "$t" ]] && continue
  ms=$(awk -v v="$t" 'BEGIN{printf "%d", v*1000}')
  RTT_SUM=$(( RTT_SUM + ms )); N_OK=$(( N_OK + 1 ))
  (( ms < RTT_MIN )) && RTT_MIN=$ms
done
if (( N_OK == 0 )); then bad "目标 5 次请求全部失败"; exit 1; fi
RTT_AVG=$(( RTT_SUM / N_OK ))
info "串行请求 平均 ${RTT_AVG}ms / 最低 ${RTT_MIN}ms"
info "这个数就是「网络 floor」——服务端处理时间下限被它盖住，测不出低于它的延迟"

curl -s --noproxy '*' -D /tmp/lgc-headers.txt -o /dev/null --max-time 8 "$TARGET_URL" 2>/dev/null
SERVER_HDR=$(grep -i '^Server:' /tmp/lgc-headers.txt 2>/dev/null | tr -d '\r')
[[ -n "$SERVER_HDR" ]] && info "响应头 $SERVER_HDR"

if (( RTT_AVG > 50 )); then
  warn "网络 floor 已 > 50ms：这轮压测的延迟数字主要是「公网延迟」，不是服务能力"
  info "想要服务端真实容量，压力机必须和服务器同内网（或直接跑在服务器上）"
fi
echo

# ---------- 5. 限流探测（核心） ----------
say "[5/5] 限流探测"

# 5a. 先用 wrk 拿「持续高速率」。
#     为什么必须这一步：curl+xargs 每个请求新起一个进程、新建一条 TCP 连接，
#     速率只有一两百 req/s，压根压不到限流阈值（500/1000），会误判成「没有限流」。
#     wrk 是 C 实现且复用连接，能真正把速率打上去。
WRK_TOTAL=""; WRK_NONOK=""; WRK_RPS=""
if command -v wrk >/dev/null 2>&1; then
  info "5a. wrk 施压 15s / 100 连接（连接复用，才能看到真实速率）"
  WRK_OUT=$(wrk -t4 -c100 -d15s --latency "$TARGET_URL" 2>&1)
  WRK_TOTAL=$(printf '%s\n' "$WRK_OUT" | sed -n 's/^ *\([0-9][0-9]*\) requests in .*/\1/p' | head -1)
  WRK_NONOK=$(printf '%s\n' "$WRK_OUT" | sed -n 's/^ *Non-2xx or 3xx responses: *\([0-9][0-9]*\).*/\1/p' | head -1)
  WRK_RPS=$(printf '%s\n' "$WRK_OUT" | sed -n 's/^Requests\/sec: *\([0-9][0-9.]*\).*/\1/p' | head -1)
  WRK_NONOK=${WRK_NONOK:-0}
  if [[ "$WRK_TOTAL" =~ ^[0-9]+$ ]]; then
    WRK_OK=$(( WRK_TOTAL - WRK_NONOK ))
    info "    尝试速率 ${WRK_RPS:-?} req/s ｜ 成功 $WRK_OK ｜ 被拒 $WRK_NONOK"
    if (( WRK_NONOK > 0 )); then
      info "    → 成功请求被压在约 $(( WRK_OK / 15 )) req/s（wrk 默认跑 15s）"
    fi
  else
    warn "    wrk 输出解析失败，本步跳过"
    WRK_TOTAL=""
  fi
else
  info "5a. 未安装 wrk，跳过高速率验证 —— 结论可信度会明显下降"
  info "    建议：brew install wrk"
fi
echo

# 5b. 再用 curl 探测拿「状态码明细」——wrk 只报数量，需要知道究竟是 429 还是 503
info "5b. curl 探测 ${PROBE_N} 个请求（并发 ${PROBE_C}），用于分辨错误类型"
PROBE_START=$(date +%s)
TMP=$(mktemp)
seq 1 "$PROBE_N" | xargs -P "$PROBE_C" -I{} curl -s --noproxy '*' --max-time 10 \
  -o /dev/null -w '%{http_code}\n' "$TARGET_URL" > "$TMP" 2>/dev/null
PROBE_ELAPSED=$(( $(date +%s) - PROBE_START ))
(( PROBE_ELAPSED < 1 )) && PROBE_ELAPSED=1
TOTAL=$(wc -l < "$TMP" | tr -d ' ')
[[ "$TOTAL" =~ ^[0-9]+$ ]] || TOTAL=0
info "    本次探测实际速率 $(( TOTAL / PROBE_ELAPSED )) req/s（低于限流阈值时结论不可靠）"
echo
printf '  %-10s %8s %10s\n' "状态码" "数量" "占比"
sort "$TMP" | uniq -c | sort -rn | while read -r cnt code; do
  printf '  %-10s %8s %9s%%\n' "$code" "$cnt" "$(awk -v a="$cnt" -v b="$TOTAL" 'BEGIN{printf "%.1f", a/b*100}')"
done
echo
if [[ -z "$TMP" ]] || (( TOTAL == 0 )); then
  bad "探测未拿到任何响应"; rm -f "$TMP"; exit 1
fi

GREY=$(grep -cE '^(5[0-9][0-9]|429|000)$' "$TMP" || true)
GREY_RATIO=$(awk -v a="$GREY" -v b="$TOTAL" 'BEGIN{printf "%.1f", a/b*100}')

WRK_BAD_RATIO="0.0"
if [[ "$WRK_TOTAL" =~ ^[0-9]+$ ]] && (( WRK_TOTAL > 0 )); then
  WRK_BAD_RATIO=$(awk -v n="$WRK_NONOK" -v t="$WRK_TOTAL" 'BEGIN{printf "%.1f", n/t*100}')
fi

if awk -v r="$GREY_RATIO" 'BEGIN{exit !(r > 5)}' || awk -v r="$WRK_BAD_RATIO" 'BEGIN{exit !(r > 1)}'; then
  bad "限流或过载已生效，这轮数字不能用"
  info "  证据：curl 探测异常 ${GREY_RATIO}% ／ wrk 被拒 ${WRK_BAD_RATIO}%"
  info "按状态码定位责任层："
  info "  429          → 应用层令牌桶（internal/transport/httpserver/middleware/）"
  info "  503          → nginx limit_req（nginx/nginx.conf，默认 limit_req_status 503）"
  info "  000 / 超时   → 连接建立失败，查 somaxconn / 安全组 / 防火墙"
  info "在目标服务器上核对容器真正拿到的参数："
  info "  docker inspect shortlink-web-1 --format '{{range .Config.Env}}{{println .}}{{end}}' \\"
  info "    | grep -E 'RATE_LIMIT|CIRCUIT'"
  info "若输出为空、或仍是 500 / 1000，说明 compose 没把环境变量透传进容器 ——"
  info "目标机上那份 docker-compose.yaml 需要有 \${RATE_LIMIT_REDIRECT_RATE:-500} 这类占位符，"
  info "仅靠 \"命令前加环境变量\" 是无效的。"
  EXIT=1
else
  ok "未发现限流迹象（curl 异常 ${GREY_RATIO}% ／ wrk 被拒 ${WRK_BAD_RATIO}%）"
fi
rm -f "$TMP" /tmp/lgc-headers.txt
echo

if (( EXIT == 0 )); then
  say "═══ 自检通过，可以开始施压 ═══"
  info "下一步：TARGET_HOST=<IP> TARGET_PORT=8080 SSH_HOST=root@<IP> \\"
  info "          bash scripts/loadtest/run-remote-loadtest.sh baseline 2000 60s redirect"
else
  say "═══ 自检未通过，先修上面的问题再压 ═══"
  info "带限流压出来的数字写进简历会被面试官一问就穿帮"
fi
echo
exit "$EXIT"
