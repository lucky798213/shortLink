# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Build / Run / Test

```bash
go build ./...              # Build all packages
go test ./...               # Run all tests
go test ./pkg/generator/... -v  # Run generator tests with verbose output
go vet ./...                # Vet code
```

## Architecture

URL shortener service built with Go, split into three tiers:

**`pkg/generator/`** — Base62 encoder. Converts uint64 auto-increment IDs to 6-character short codes. Charset: `0-9a-zA-Z` (digits, lowercase, uppercase). Left-pads with `'0'`.

**`rpc/`** — gRPC server (port 50051). Layers:
- `grpc/shortUrl.go` — gRPC handler implementing `ShortUrlServer` interface from `proto/`
- `service/shortUrl.go` — Business logic: create (insert row → encode ID → update short_code), LocalCache/Redis/singleflight lookup, expiration status, soft delete, async visit logging, and stats
- `repository/shortUrl.go` — Repository interface (`ShortUrlRepo`)
- `repository/dao/shortUrl.go` — GORM + MySQL implementation
- `repository/dao/shortUrlVisit.go` — visit logging and stats aggregation
- `repository/cache/shortUrl.go` — Redis + Noop cache implementations for short code mappings
- `config/config.template.yaml` — Config template (db.dsn, grpc.addr)

**`web/`** — Gin HTTP server (port 8080). Routes:
- `POST /api/short-links` → gRPC `CreateShortUrl` → create a short link with optional `expire_at`
- `GET /api/short-links/:code` → gRPC `GetShortUrl` → short link detail
- `GET /api/short-links/:code/stats` → gRPC `GetShortUrlStats` → visit stats
- `DELETE /api/short-links/:code` → gRPC `DeleteShortUrl` → soft delete
- `GET /:code` → gRPC `GetOriginUrl` → 301 redirect, 404 not found, or 410 expired

**`proto/`** — Protobuf service definition and hand-written Go gRPC stubs (protoc not available in build pipeline; if you add RPCs, update both `.proto` and the Go stub files).

**`scripts/mysql/init.sql`** — Creates `short_url` DB, `short_urls` table, and `short_url_visits` table.

Configuration via Viper from YAML. Logging via `log/slog`.

### Docker Compose

Four services: `mysql` (8.0), `redis` (7), `rpc` (depends on mysql and redis), `web` (depends on rpc, exposes :8080). Dockerfiles at `Dockerfile.rpc` and `Dockerfile.web`.
