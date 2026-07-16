# AGENTS.md

This file provides guidance to Codex when working in this repository.

## Build / Run / Test

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
buf lint
buf generate
```

Run the two processes from the repository root:

```bash
go run ./cmd/rpc
go run ./cmd/web
```

All code comments must be written in Chinese.

## Architecture

The dependency direction is `transport -> application -> domain`. Infrastructure implements ports declared by the application layer.

- `api/shortlink/v1/`: versioned protobuf contract and generated Go code. Change the `.proto`, then run Buf; never hand-edit generated files.
- `cmd/rpc/`, `cmd/web/`: composition roots. They load config, wire dependencies, start servers, and coordinate graceful shutdown.
- `internal/shortlink/`: domain models, status semantics, validation, errors, and Base62 code logic.
- `internal/shortlink/app/`: application use cases split into `Creator`, `Resolver`, `Manager`, `StatsService`, and `VisitWriter`. Port interfaces live in `ports.go`.
- `internal/transport/grpcserver/`: protobuf/gRPC adapter.
- `internal/transport/httpserver/`: Gin adapter, router, handlers, and middleware.
- `internal/infra/mysql/`: sharded link store, MySQL ID allocator, visit store, and shard strategy.
- `internal/infra/cache/`: Redis, local, and noop cache implementations.
- `internal/jobs/`: expired-link cleanup and Bloom rebuild workers.
- `internal/platform/`: Bloom filter, etcd discovery, logging, and rate limiting.
- `internal/config/`: typed RPC/Web configuration, defaults, environment overrides, and validation.
- `configs/`: default `rpc.yaml` and `web.yaml` files.

The MySQL write path allocates an ID first, derives the Base62 short code, and writes the complete row once. A buffered batch may span shards, so `BatchCreate` must remain atomic across all touched tables.

The read path is LocalCache -> Redis -> Bloom -> singleflight -> MySQL. Shared lookups have their own timeout and must not inherit cancellation from the first caller.

## Protocol and schema

- The public RPC API is `shortlink.v1.ShortLinkService`.
- Protobuf responses use the `ShortLinkStatus` enum; transport errors use standard gRPC status codes.
- `scripts/mysql/init.sql` creates 64 shard tables, the ID allocator table, and the visit table.
- If `sharding.count` changes, schema provisioning must create the same number of physical tables.

## Docker Compose

Compose starts MySQL, Redis, etcd, RPC, Web, and nginx. The RPC and Web images build `./cmd/rpc` and `./cmd/web` respectively.
