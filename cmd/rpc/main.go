package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	proto "short_url/api/shortlink/v1"
	"short_url/internal/config"
	cacheinfra "short_url/internal/infra/cache"
	mysqlstore "short_url/internal/infra/mysql"
	"short_url/internal/jobs"
	"short_url/internal/platform/bloom"
	"short_url/internal/platform/discovery"
	"short_url/internal/platform/logging"
	shortlinkapp "short_url/internal/shortlink/app"
	"short_url/internal/transport/grpcserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("RPC 服务退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	defer logging.Sync()

	cfg, err := config.LoadRPC(os.Getenv("SHORT_URL_RPC_CONFIG"))
	if err != nil {
		return err
	}
	if _, err := logging.Init(cfg.Logger.Level, cfg.Logger.Development); err != nil {
		slog.Warn("按配置初始化日志失败，继续使用默认日志", "err", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := gorm.Open(mysql.Open(cfg.DB.DSN), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("连接数据库: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取数据库连接池: %w", err)
	}
	defer sqlDB.Close()

	shortURLCache, redisClient := initShortURLCache(cfg.Redis)
	if redisClient != nil {
		defer redisClient.Close()
	}

	var bloomFilter bloom.Filter = bloom.AllowAllFilter{}
	if redisClient != nil && cfg.Bloom.Enabled {
		bloomFilter = bloom.NewRedisFilter(redisClient, cfg.Bloom.Key, cfg.Bloom.Bits, cfg.Bloom.Hashes)
	}

	allocator := mysqlstore.NewMySQLIDAllocator(db, cfg.IDAllocator.Step)
	repo := mysqlstore.NewShardedLinkStore(db, cfg.Sharding.Count)
	visitStore := mysqlstore.NewShortUrlVisitRepo(db)
	service := shortlinkapp.NewService(repo, shortlinkapp.Options{
		RemoteCache: shortURLCache,
		LocalCache:  cacheinfra.NewLocalShortUrlCache(cfg.Cache.LocalMaxEntries),
		Cache: shortlinkapp.CacheOptions{
			DefaultTTL:    cfg.Cache.DefaultTTL,
			NotFoundTTL:   cfg.Cache.NotFoundTTL,
			LocalTTL:      cfg.Cache.LocalTTL,
			JitterRatio:   cfg.Cache.JitterRatio,
			LookupTimeout: cfg.Cache.LookupTimeout,
		},
		IDAllocator: allocator,
		Create: shortlinkapp.CreateOptions{
			Buffered:       true,
			QueueSize:      cfg.WriteBuffer.QueueSize,
			BatchSize:      cfg.WriteBuffer.BatchSize,
			FlushInterval:  cfg.WriteBuffer.FlushInterval,
			EnqueueTimeout: cfg.WriteBuffer.EnqueueTimeout,
		},
		BloomFilter: bloomFilter,
		VisitStore:  visitStore,
		Visit: shortlinkapp.VisitOptions{
			QueueSize:     cfg.Stats.QueueSize,
			WorkerCount:   cfg.Stats.WorkerCount,
			BatchSize:     cfg.Stats.BatchSize,
			FlushInterval: cfg.Stats.FlushInterval,
			IPHashSalt:    cfg.Stats.IPHashSalt,
		},
	})
	defer shutdownService(service)

	jobs.StartExpiredCleaner(ctx, repo, shortURLCache, jobs.ExpiredCleanerOptions{
		Interval:  cfg.Cleanup.Interval,
		BatchSize: cfg.Cleanup.BatchSize,
	})
	if redisClient != nil && cfg.Bloom.Enabled {
		jobs.StartBloomRebuilder(ctx, repo, bloomFilter, jobs.BloomRebuilderOptions{
			Interval:  cfg.Bloom.RebuildInterval,
			BatchSize: cfg.Bloom.RebuildBatchSize,
		})
	}

	listener, err := net.Listen("tcp", cfg.GRPC.Addr)
	if err != nil {
		return fmt.Errorf("监听 gRPC 地址 %s: %w", cfg.GRPC.Addr, err)
	}
	grpcServer := grpc.NewServer()
	proto.RegisterShortLinkServiceServer(grpcServer, grpcserver.NewShortUrlServer(service))
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	if cfg.Etcd.Enabled {
		etcdClient, err := discovery.NewEtcdClient(cfg.Etcd.Endpoints, cfg.Etcd.DialTimeout)
		if err != nil {
			slog.Warn("etcd 客户端初始化失败，跳过服务注册", "err", err)
		} else {
			defer etcdClient.Close()
			registrar := discovery.NewRegistrar(etcdClient, cfg.Service.Name, cfg.Service.Addr, cfg.Etcd.LeaseTTL)
			if err := registrar.Start(ctx); err != nil {
				slog.Warn("服务注册到 etcd 失败", "err", err)
			}
		}
	}

	go stopGRPCServer(ctx, grpcServer, healthServer)
	slog.Info("gRPC 服务启动中", "addr", cfg.GRPC.Addr)
	serveErr := grpcServer.Serve(listener)
	if serveErr != nil && ctx.Err() == nil {
		return fmt.Errorf("运行 gRPC 服务: %w", serveErr)
	}
	return nil
}

func shutdownService(service *shortlinkapp.Service) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		slog.Warn("业务后台任务关闭失败", "err", err)
	}
}

func stopGRPCServer(ctx context.Context, server *grpc.Server, healthServer *health.Server) {
	<-ctx.Done()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("gRPC 优雅关闭超时，强制停止")
		server.Stop()
	}
}

func initShortURLCache(cfg config.Redis) (shortlinkapp.Cache, *redis.Client) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis 连接失败，短链接缓存降级为 Noop", "addr", cfg.Addr, "err", err)
		_ = client.Close()
		return cacheinfra.NewNoopShortUrlCache(), nil
	}
	slog.Info("Redis 连接成功", "addr", cfg.Addr)
	return cacheinfra.NewRedisShortUrlCache(client), client
}
