# ShortLink Load Test Runbook

This runbook saves evidence files so the benchmark is reproducible and reviewable.

Run from:

```bash
cd /Users/liangzhancheng/GolandProjects/shortLink
```

## 1. Prepare

```bash
mkdir -p docs/load-test-artifacts/$(date +%Y%m%d-%H%M%S)
export ARTIFACT_DIR=$(ls -td docs/load-test-artifacts/* | head -n 1)

go install github.com/codesenberg/bombardier@latest
export BOMBARDIER=/Users/liangzhancheng/go/bin/bombardier
```

## 2. Start Services

```bash
docker compose up -d --build
docker compose ps > "$ARTIFACT_DIR/docker-compose-ps-before.txt"
docker info > "$ARTIFACT_DIR/docker-info.txt"
curl -sS http://localhost:8080/healthz > "$ARTIFACT_DIR/healthz-before.json"
curl -sS http://localhost:8080/readyz > "$ARTIFACT_DIR/readyz-before.json"
```

## 3. Start Unrestricted Load-Test Web Container

```bash
docker compose stop nginx web
docker rm -f shortlink-web-loadtest || true

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

for i in $(seq 1 30); do
  curl -fsS http://localhost:8080/readyz >/dev/null && break
  sleep 1
done
```

## 4. Create Redirect Seed

```bash
curl -sS \
  -X POST http://localhost:8080/api/short-links \
  -H 'content-type: application/json' \
  -d "{\"origin_url\":\"https://example.com/load-test-$(date +%s)\"}" \
  | tee "$ARTIFACT_DIR/seed-create-response.json"

export SHORT_CODE=$(sed -n 's/.*"short_code":"\([^"]*\)".*/\1/p' "$ARTIFACT_DIR/seed-create-response.json")
echo "$SHORT_CODE" | tee "$ARTIFACT_DIR/short-code.txt"
```

## 5. Apply 2C2G Approximation

```bash
docker update --cpuset-cpus 0-1 --memory 384m --memory-swap 384m shortlink-web-loadtest
docker update --cpuset-cpus 0-1 --memory 384m --memory-swap 384m shortlink-rpc-1
docker update --cpuset-cpus 0-1 --memory 1024m --memory-swap 1024m shortlink-mysql-1
docker update --cpuset-cpus 0-1 --memory 128m --memory-swap 128m shortlink-redis-1
docker update --cpuset-cpus 0-1 --memory 128m --memory-swap 128m shortlink-etcd-1
docker restart shortlink-web-loadtest

docker inspect shortlink-web-loadtest shortlink-rpc-1 shortlink-mysql-1 shortlink-redis-1 shortlink-etcd-1 \
  --format '{{.Name}} cpuset={{.HostConfig.CpusetCpus}} memory={{.HostConfig.Memory}} swap={{.HostConfig.MemorySwap}}' \
  > "$ARTIFACT_DIR/docker-resource-limits-2c2g.txt"

for i in $(seq 1 30); do
  curl -fsS http://localhost:8080/readyz >/dev/null && break
  sleep 1
done
```

## 6. Run 60s Validation Tests

Redirect:

```bash
"$BOMBARDIER" \
  -c 1200 \
  -d 60s \
  -r 12000 \
  -l \
  -o json \
  -p result \
  -t 5s \
  "http://localhost:8080/$SHORT_CODE" \
  | tee "$ARTIFACT_DIR/2c2g-redirect-12000qps-60s.json"
```

Create:

```bash
"$BOMBARDIER" \
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
  http://localhost:8080/api/short-links \
  | tee "$ARTIFACT_DIR/2c2g-create-800qps-60s.json"
```

## 7. Verify

```bash
curl -sS http://localhost:8080/healthz | tee "$ARTIFACT_DIR/healthz-after.json"
curl -sS http://localhost:8080/readyz | tee "$ARTIFACT_DIR/readyz-after.json"

docker logs --tail=1000 shortlink-web-loadtest > "$ARTIFACT_DIR/web-loadtest-logs-tail.txt" 2>&1
docker compose logs --tail=1000 rpc > "$ARTIFACT_DIR/rpc-logs-tail.txt" 2>&1

rg -i 'error|panic|fatal|failed|timeout|drop|full|queue|失败|丢|满' \
  "$ARTIFACT_DIR/web-loadtest-logs-tail.txt" \
  "$ARTIFACT_DIR/rpc-logs-tail.txt" \
  | tee "$ARTIFACT_DIR/error-log-scan.txt" || true

docker stats --no-stream > "$ARTIFACT_DIR/docker-stats-after.txt"

docker compose exec -T mysql mysql -uroot -proot -N -e \
  "SELECT COUNT(*) FROM short_url.short_url_visits WHERE short_code='${SHORT_CODE}';" \
  > "$ARTIFACT_DIR/mysql-visit-count.txt"
```

## 8. Restore

```bash
docker rm -f shortlink-web-loadtest

docker update --cpuset-cpus 0-9 --memory 7g --memory-swap 7g shortlink-rpc-1 shortlink-mysql-1 shortlink-redis-1 shortlink-etcd-1

docker compose start web nginx
docker compose ps > "$ARTIFACT_DIR/docker-compose-ps-after.txt"
curl -sS http://localhost:8080/readyz > "$ARTIFACT_DIR/readyz-restored.json"
```

