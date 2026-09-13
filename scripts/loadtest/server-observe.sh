#!/usr/bin/env bash
# ShortLink 目标服务器侧指标采集器（在【目标服务器】上运行）
#
# 用法：
#   INTERVAL=1 DURATION=180 OUT=/tmp/observe.csv bash scripts/loadtest/server-observe.sh
#
#   INTERVAL  采样间隔秒，默认 1
#   DURATION  总时长秒，默认 0 表示一直采到被 kill
#   OUT       输出 CSV 路径
#   DOCKER_STATS  是否采集容器指标（需要 docker），默认 1
#   DOCKER_EVERY  容器指标采集间隔（秒），默认 5，避免 docker stats 自身开销
#
# 只读 /proc 与 docker inspect/stats，不会修改服务器任何状态。
# 产出 CSV 供压测后与 k6 结果对照，定位「是业务慢还是机器满了」。

set -uo pipefail

INTERVAL="${INTERVAL:-1}"
DURATION="${DURATION:-0}"
OUT="${OUT:-./loadtest-observe.csv}"
DOCKER_STATS="${DOCKER_STATS:-1}"
DOCKER_EVERY="${DOCKER_EVERY:-5}"

have() { command -v "$1" >/dev/null 2>&1; }

# ---------- 采样基准 ----------
read_cpu() { # 输出 "idle total"
  awk '/^cpu /{idle=$5+$6; total=0; for(i=2;i<=NF;i++) total+=$i; print idle, total; exit}' /proc/stat
}
read -r P_IDLE P_TOTAL <<<"$(read_cpu)"

IFACE=""
if [[ -r /proc/net/route ]]; then
  IFACE=$(awk '$2=="00000000"{print $1; exit}' /proc/net/route)
fi
[[ -n "$IFACE" && -r "/sys/class/net/$IFACE/statistics/rx_bytes" ]] || IFACE="eth0"
read_net() { # 输出 "rx tx"
  local rx tx
  rx=$(cat "/sys/class/net/$IFACE/statistics/rx_bytes" 2>/dev/null || echo 0)
  tx=$(cat "/sys/class/net/$IFACE/statistics/tx_bytes" 2>/dev/null || echo 0)
  echo "$rx $tx"
}
read -r P_RX P_TX <<<"$(read_net)"

# ---------- 容器名 ----------
find_container() { # $1=关键字
  docker ps --format '{{.Names}}' 2>/dev/null | grep -i "$1" | head -1
}
MYSQL_C=""; REDIS_C=""
if [[ "$DOCKER_STATS" == "1" ]] && have docker; then
  MYSQL_C=$(find_container mysql)
  REDIS_C=$(find_container redis)
fi

echo "# host=$(hostname) cores=$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo ?) iface=$IFACE start=$(date -Is)" >"$OUT"
echo "ts,load1,load5,cpu_busy_pct,mem_used_mb,mem_avail_mb,swap_used_mb,net_rx_kbps,net_tx_kbps,tcp_estab,tcp_tw,docker_cpu_pct,docker_mem_mb,redis_ops,redis_clients,mysql_running,mysql_connected" >>"$OUT"

START_TS=$(date +%s)
LAST_DOCKER_TS=0
trap 'echo "# stopped at $(date -Is)" >>"$OUT"; exit 0' TERM INT

while :; do
  NOW=$(date +%s)
  NOW_MS=$(date +%s%N)

  # CPU 使用率（本区间）
  read -r C_IDLE C_TOTAL <<<"$(read_cpu)"
  CPU_BUSY=$(awk -v i1="$P_IDLE" -v t1="$P_TOTAL" -v i2="$C_IDLE" -v t2="$C_TOTAL" \
    'BEGIN{d=t2-t1; if(d<=0){print "0.00"} else {printf "%.2f", (1-(i2-i1)/d)*100}}')
  P_IDLE=$C_IDLE; P_TOTAL=$C_TOTAL

  # 内存
  read -r MEM_USED MEM_AVAIL SWAP_USED <<<"$(awk '
    /MemTotal/{t=$2} /MemAvailable/{a=$2} /SwapTotal/{st=$2} /SwapFree/{sf=$2}
    END{printf "%.0f %.0f %.0f", (t-a)/1024, a/1024, (st-sf)/1024}' /proc/meminfo)"

  # 网卡速率
  read -r C_RX C_TX <<<"$(read_net)"
  NET_RX=$(awk -v a="$P_RX" -v b="$C_RX" -v s="$INTERVAL" 'BEGIN{printf "%.1f", (b-a)/1024/s}')
  NET_TX=$(awk -v a="$P_TX" -v b="$C_TX" -v s="$INTERVAL" 'BEGIN{printf "%.1f", (b-a)/1024/s}')
  P_RX=$C_RX; P_TX=$C_TX

  # TCP 状态
  if [[ -r /proc/net/tcp ]]; then
    read -r TCP_ESTAB TCP_TW <<<"$(awk 'NR>1{s=$4; if(s=="01")e++; else if(s=="06")tw++} END{printf "%d %d", e+0, tw+0}' /proc/net/tcp)"
  else
    TCP_ESTAB=0; TCP_TW=0
  fi

  # 容器指标：每 DOCKER_EVERY 秒一次
  D_CPU=""; D_MEM=""; R_OPS=""; R_CLI=""; M_RUN=""; M_CONN=""
  if [[ "$DOCKER_STATS" == "1" ]] && have docker && (( NOW - LAST_DOCKER_TS >= DOCKER_EVERY )); then
    LAST_DOCKER_TS=$NOW
    if [[ -n "$MYSQL_C$REDIS_C" ]]; then
      SNAP=$(docker stats --no-stream --format '{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}' \
        $(docker ps -q --filter 'name=shortlink' 2>/dev/null) 2>/dev/null)
      D_CPU=$(printf '%s\n' "$SNAP" | awk -F'|' '{gsub(/%/,"",$2); c+=$2} END{printf "%.2f", c+0}')
      D_MEM=$(printf '%s\n' "$SNAP" | awk -F'|' '{
        split($3,a," / "); v=a[1]; gsub(/[A-Za-z]/,"",v);
        if ($3 ~ /GiB/) v*=1024; m+=v} END{printf "%.0f", m+0}')
    fi
    if [[ -n "$REDIS_C" ]]; then
      RINFO=$(docker exec "$REDIS_C" redis-cli info 2>/dev/null)
      R_OPS=$(printf '%s\n' "$RINFO" | awk -F: '/^instantaneous_ops_per_sec/{print $2+0}')
      R_CLI=$(printf '%s\n' "$RINFO" | awk -F: '/^connected_clients/{print $2+0}')
    fi
    if [[ -n "$MYSQL_C" ]]; then
      MST=$(docker exec "$MYSQL_C" mysql -uroot -proot -N -e \
        "SHOW GLOBAL STATUS WHERE Variable_name IN ('Threads_running','Threads_connected');" 2>/dev/null)
      M_RUN=$(printf '%s\n' "$MST" | awk '$1=="Threads_running"{print $2+0}')
      M_CONN=$(printf '%s\n' "$MST" | awk '$1=="Threads_connected"{print $2+0}')
    fi
  fi

  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$(date +%H:%M:%S)" \
    "$(awk '{print $1}' /proc/loadavg)" \
    "$(awk '{print $2}' /proc/loadavg)" \
    "$CPU_BUSY" "$MEM_USED" "$MEM_AVAIL" "$SWAP_USED" \
    "$NET_RX" "$NET_TX" "$TCP_ESTAB" "$TCP_TW" \
    "$D_CPU" "$D_MEM" "$R_OPS" "$R_CLI" "$M_RUN" "$M_CONN" >>"$OUT"

  (( DURATION > 0 )) && (( NOW - START_TS >= DURATION )) && { echo "# stopped at $(date -Is)" >>"$OUT"; break; }

  # 对齐到秒边界，减小累计漂移
  SLEEP=$(awk -v n="$NOW_MS" -v i="$INTERVAL" 'BEGIN{s=(i*1e9-(n% (i*1e9)))/1e9; if(s<=0||s>i) s=i; printf "%.3f", s}')
  sleep "$SLEEP"
done
