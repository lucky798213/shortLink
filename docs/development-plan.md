# 短链接项目开发计划

## 当前状态

项目已经从按进程目录堆叠代码的三层结构，重构为领域、应用、传输和基础设施边界清晰的模块化单体，并通过 gRPC 拆分 Web 网关与 RPC 进程。

已完成能力：

- 6 位 Base62 短码与 MySQL 全局号段分配。
- 64 张短链接分片表，跨分片批量写入保持事务原子性。
- LocalCache、Redis、Bloom Filter、singleflight 和 MySQL 的分层读路径。
- 创建、详情、跳转、软删除、过期状态和访问统计。
- 异步批量创建、异步访问日志、过期清理和 Bloom 重建。
- HTTP/gRPC 标准错误映射、健康检查、限流、熔断和 etcd 服务发现。
- 类型化配置、优雅关闭、竞态检测和关键基础设施测试。
- Buf 管理的 `shortlink.v1` Protobuf 契约与官方生成代码。

## 已完成的架构重构

- [x] 把领域模型、状态和输入校验移到 `internal/shortlink`。
- [x] 把业务逻辑拆为 `Creator`、`Resolver`、`Manager`、`StatsService` 和 `VisitWriter`。
- [x] 在应用层声明存储、缓存、ID 分配、Bloom 和访问日志端口。
- [x] 把 MySQL、Redis、本地缓存实现移到 `internal/infra`。
- [x] 把 gRPC、HTTP 适配器移到 `internal/transport`。
- [x] 把进程入口收敛到 `cmd/rpc` 和 `cmd/web`。
- [x] 删除旧的单表仓储和手写 protobuf stub。
- [x] 用强类型配置替代启动代码中的全局 `viper.Get(...)`。
- [x] 让创建缓冲、访问日志 worker、数据库、Redis 和 gRPC 客户端都可关闭。
- [x] 修复跨分片批量写的部分提交风险和 singleflight 首调用方取消传播问题。

## 后续演进

### P1：可观测性

- 为创建、跳转、缓存命中、回源、队列丢弃和后台任务增加 Prometheus 指标。
- 将 HTTP Request ID 通过 gRPC metadata 传递到 RPC 日志。
- 增加 OpenTelemetry trace，串联 Web、RPC、Redis 和 MySQL。

### P1：数据与容量治理

- 将 `sharding.count` 与建表工具绑定，启动时验证物理分片数量。
- 为号段剩余容量、创建队列、访问队列和 Bloom 误判率增加告警。
- 评估访问明细的按月分表、归档和统计预聚合。

### P2：安全与协议

- 为内部 gRPC 增加 TLS 或 mTLS。
- 将 API Key 替换为可轮换的凭据体系，并避免在静态 YAML 中保存生产密钥。
- 为管理接口增加分页、批量操作和审计记录。

### P2：交付

- [x] 在 CI 中增加 `go test -race ./...` 和 `buf lint`。
- [ ] 在 CI 中增加 Docker 镜像构建。
- 增加基于 Docker Compose 的端到端冒烟测试。
- 为 schema migration 引入明确的迁移工具和回滚策略。

## 验收命令

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
buf lint
buf generate
docker compose config
```
