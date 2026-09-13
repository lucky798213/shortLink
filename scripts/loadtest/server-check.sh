#!/usr/bin/env bash
# ShortLink 服务器压测前置体检（只读，不修改任何系统参数）
#
# 在【目标服务器】上执行：
#   bash scripts/loadtest/server-check.sh
#
# 输出一份体检报告：机器规格、文件描述符、内核网络参数、端口暴露情况、
# 容器资源限制，并对每一项给出「是否适合开高并发压测」的结论与建议值。

set -uo pipefail

ok()   { printf '  \033[32m[OK]\033[0m   %s\n' "$1"; }
warn() { printf '  \033[33m[WARN]\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31m[FAIL]\033[0m %s\n' "$1"; }
info() { printf '         %s\n' "$1"; }

hr() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }

hr "1. 机器规格"
CPU_CORES=$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo "?")
MEM_MB=$(awk '/MemTotal/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo "?")
SWAP_MB=$(awk '/SwapTotal/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 0)
DISK_FREE=$(df -h / 2>/dev/null | awk 'NR==2{print $4}')
info "CPU 核数      : ${CPU_CORES}"
info "内存          : ${MEM_MB} MB"
info "Swap          : ${SWAP_MB} MB"
info "根分区可用    : ${DISK_FREE}"
info "内核          : $(uname -sr)"
if [[ "$CPU_CORES" == "?" || "$MEM_MB" == "?" ]]; then
  warn "非 Linux 或 /proc 不可读，本脚本按 Linux 服务器设计"
elif (( MEM_MB < 1800 )); then
  warn "内存偏小（${MEM_MB}MB）。压测时 DROP 缓存可能触发 OOM，"
  info "务必确认 docker-compose 已给 mysql 降载（--innodb_buffer_pool_size=128M）"
else
  ok "资源规格满足压测要求"
fi

hr "2. 容器资源限制与互抢风险"
if command -v docker >/dev/null 2>&1; then
  ok "docker 已安装：$(docker --version 2>/dev/null)"
  RUNNING=$(docker ps --format '{{.Names}}' 2>/dev/null | tr '\n' ' ')
  info "运行中容器：${RUNNING:-（无）}"
  if [[ "$RUNNING" == *"shortlink"* ]]; then
    ok "shortlink 服务栈在运行"
    docker inspect $(docker ps -q --filter 'name=shortlink') \
      --format '         {{.Name}} cpuset={{.HostConfig.CpusetCpus}} mem={{.HostConfig.Memory}}' 2>/dev/null
  else
    warn "未发现 shortlink 容器，请先在目标机 docker compose up -d"
  fi
else
  bad "未安装 docker，无法用 compose 形态压测"
fi

hr "3. 文件描述符上限（高并发第一瓶颈）"
NPROC=$(ulimit -n)
info "当前 shell ulimit -n = ${NPROC}"
if [[ "$NPROC" =~ ^[0-9]+$ ]] && (( NPROC < 65535 )); then
  warn "低于 65535。若压力机与目标机同机压测，会先报 'too many open files'"
  info "建议：ulimit -n 65535（临时）；永久改 /etc/security/limits.conf 的 nofile"
else
  ok "文件描述符上限足够"
fi
FS_FILE_MAX=$(cat /proc/sys/fs/file-max 2>/dev/null || echo "?")
info "/proc/sys/fs/file-max = ${FS_FILE_MAX}"

hr "4. 内核网络参数（accept 队列 / TIME_WAIT 回收 / 端口范围）"
check_sysctl() {
  local key="$1" expect="$2" desc="$3" op="$4"
  local val
  val=$(sysctl -n "$key" 2>/dev/null || echo "?")
  local verdict
  verdict=$([[ "$val" == "?" ]] && echo "unknown" || awk -v v="$val" -v e="$expect" -v o="$op" 'BEGIN{print (o=="ge" ? (v>=e?"ok":"low") : (v<=e?"ok":"high"))}')
  case "$verdict" in
    ok)      ok   "${key} = ${val}  （${desc}）" ;;
    low)     warn "${key} = ${val} < 建议 ${expect}  （${desc}）" ;;
    high)    warn "${key} = ${val} > 建议 ${expect}  （${desc}）" ;;
    unknown) warn "${key} 读取失败" ;;
  esac
}
check_sysctl net.core.somaxconn            4096  "accept 队列长度，压测时 8080 会瞬时堆积" ge
check_sysctl net.ipv4.tcp_max_syn_backlog  4096  "SYN 半连接队列" ge
check_sysctl net.ipv4.tcp_tw_reuse         1     "TIME_WAIT 复用，短连接压测必需"  ge
check_sysctl net.ipv4.tcp_fin_timeout      30    "FIN 等待时长"                       le
check_sysctl net.ipv4.ip_local_port_range  ""    "临时端口范围"                        ge
info "临时端口范围：$(sysctl -n net.ipv4.ip_local_port_range 2>/dev/null)"
info "建议（需 sudo，执行前自行确认）："
info "  sysctl -w net.core.somaxconn=4096 net.ipv4.tcp_max_syn_backlog=4096 \\"
info "             net.ipv4.tcp_tw_reuse=1 net.ipv4.tcp_fin_timeout=15 \\"
info "             net.ipv4.ip_local_port_range='10240 65535'"
info "  持久化：写入 /etc/sysctl.d/99-loadtest.conf"

hr "5. 端口暴露情况（外部压力机能否打进来）"
if command -v ss >/dev/null 2>&1; then
  info "监听中的相关端口："
  ss -lntp 2>/dev/null | awk 'NR==1 || /:(8080|8888|50051|80|443)\s/' | sed 's/^/         /'
  if ss -lnt 2>/dev/null | grep -qE '127\.0\.0\.1:8080\b'; then
    bad "web 服务只绑定在 127.0.0.1:8080 —— 外部压力机打不进来！"
    info "解法A（推荐）：在目标机用 WEB_BIND=0.0.0.0 重建 web 容器"
    info "  WEB_BIND=0.0.0.0 docker compose up -d web"
    info "解法B：改打 nginx 的 8888 端口（注意 nginx 有 100r/s 的 limit_req）"
    info "解法C：SSH 隧道（仅适合冒烟，不适合高 QPS）"
  else
    ok "8080 已对外监听，压力机可直连"
  fi
  if ss -lnt 2>/dev/null | grep -qE ':8888\b'; then
    warn "nginx 8888 已暴露。注意 nginx limit_req 限制为 100r/s/IP、burst 200，"
    info "走 nginx 压测时你会先打到 nginx 限流，不要把它误判为业务瓶颈"
  fi
else
  warn "无 ss 命令，跳过端口检查（可 yum/apt install iproute2）"
fi

hr "6. 防火墙 / 安全组提醒"
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  warn "ufw 处于开启状态，确认已放行压力机来源 IP 与压测端口"
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  warn "firewalld 在运行，确认已放行压测端口"
else
  info "未检测到本机防火墙；云厂商安全组仍需在控制台放行压测端口"
fi
info "提示：压测结束后请立即关闭临时放行的端口"

hr "7. 数据面依赖（MySQL / Redis）"
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q mysql; then
  MYSQL_C=$(docker ps --format '{{.Names}}' | grep mysql | head -1)
  MAXCONN=$(docker exec "$MYSQL_C" mysql -uroot -proot -N -e "SELECT @@max_connections;" 2>/dev/null || echo "?")
  info "MySQL max_connections = ${MAXCONN}"
else
  warn "未发现 mysql 容器"
fi
if docker ps --format '{{.Names}}' 2>/dev/null | grep -q redis; then
  REDIS_C=$(docker ps --format '{{.Names}}' | grep redis | head -1)
  MAXCLI=$(docker exec "$REDIS_C" redis-cli config get maxclients 2>/dev/null | tail -1 || echo "?")
  RDB_SAVE=$(docker exec "$REDIS_C" redis-cli config get save 2>/dev/null | tail -1 || echo "?")
  info "Redis maxclients = ${MAXCLI}"
  info "Redis save 策略 = ${RDB_SAVE}"
  info "提示：压测中 RDB 落盘会周期性抖动，必要时压测期间关闭持久化"
else
  warn "未发现 redis 容器"
fi

hr "体检结论"
cat <<'EOF'
  必须处理（否则数据不可信）：
    1. web 只绑 127.0.0.1 -> 用 WEB_BIND=0.0.0.0 重建，或改走 nginx
    2. Web 层自带限流（默认创建 50/s、跳转 500/s）-> 压测容器必须用
       RATE_LIMIT_API_RATE / RATE_LIMIT_REDIRECT_RATE 等环境变量放开
    3. 压测进程不要跑在目标机上 —— 用独立压力机
  建议处理（影响极限吞吐）：
    4. ulimit -n / somaxconn / tcp_tw_reuse
    5. 云安全组放行压测端口，测完立刻关闭
EOF
