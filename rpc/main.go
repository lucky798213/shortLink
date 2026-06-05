package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	bloompkg "short_url/pkg/bloom"
	"short_url/pkg/discovery"
	"short_url/pkg/logging"
	"short_url/proto"
	grpcserver "short_url/rpc/grpc"
	"short_url/rpc/job"
	cachepkg "short_url/rpc/repository/cache"
	"short_url/rpc/repository/dao"
	"short_url/rpc/service"
)

func main() {
	//程序退出前刷新日志。
	defer logging.Sync()

	//监听停止信号
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// —————— 第 1 步：加载配置 ——————
	// Viper 会按顺序在以下目录搜索 config.yaml：
	//   ./rpc/config/     → 本地开发环境
	//   ./                → 当前目录（兜底）
	//   /etc/short_url/   → Docker 容器内（Dockerfile 将配置拷贝到此目录）
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./rpc/config")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/short_url")
	if err := viper.ReadInConfig(); err != nil {
		// 配置文件读取失败是致命错误，直接退出。
		// 不使用 panic 是因为需要给用户清晰的错误信息。
		slog.Error("读取配置文件失败", "err", err)
		os.Exit(1)
	}
	if _, err := logging.Init(viper.GetString("logger.level"), viper.GetBool("logger.development")); err != nil {
		slog.Warn("按配置初始化日志失败，继续使用默认日志", "err", err)
	}

	// —————— 第 2 步：连接数据库 ——————
	// DSN 格式：user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
	// parseTime=True 让 GORM 自动将 MySQL 的 DATETIME 转为 Go 的 time.Time
	// loc=Local 使用系统本地时区
	db, err := gorm.Open(mysql.Open(viper.GetString("db.dsn")), &gorm.Config{})
	if err != nil {
		slog.Error("数据库连接失败", "err", err)
		os.Exit(1)
	}
	slog.Info("数据库连接成功")

	// —————— 第 3 步：初始化 Redis 缓存 ——————
	shortURLCache, redisClient := initShortURLCache()
	if redisClient != nil {
		defer redisClient.Close()
	}

	//初始化 Bloom Filter（布隆过滤器）
	var bloomFilter bloompkg.Filter = bloompkg.AllowAllFilter{}
	if redisClient != nil && viper.GetBool("bloom.enabled") {
		bloomFilter = bloompkg.NewRedisFilter(
			redisClient,
			viper.GetString("bloom.key"),
			viper.GetUint64("bloom.bits"),
			viper.GetUint64("bloom.hashes"),
		)
	}

	// —————— 第 4 步：组装依赖链 ——————
	// 依赖注入顺序：DB → Repository → Service → gRPC Handler
	// 每层只依赖它的下一层接口，不越层调用。

	//短链接 ID 发号器
	//给每一条新短链接分配一个唯一的数字 ID
	allocator := dao.NewMySQLIDAllocator(db, viper.GetUint64("id_allocator.step"))

	//创建分片存储的短链接数据库层
	repo := dao.NewShardedShortUrlRepo(db, viper.GetInt("sharding.count"), allocator) // 分片数据访问层

	//创建访问日志存储层（统计点击量）
	visitRepo := dao.NewShortUrlVisitRepo(db) // 访问日志数据访问层

	//创建真正的业务服务（核心）
	svc := service.NewShortUrlServiceWithOptions(repo, service.ShortUrlServiceOptions{
		RemoteCache:          shortURLCache,
		LocalCache:           cachepkg.NewLocalShortUrlCache(viper.GetInt("cache.local_max_entries")),
		DefaultCacheTTL:      viper.GetDuration("cache.default_ttl"),
		NotFoundCacheTTL:     viper.GetDuration("cache.not_found_ttl"),
		LocalCacheTTL:        viper.GetDuration("cache.local_ttl"),
		CacheJitterRatio:     viper.GetFloat64("cache.jitter_ratio"),
		VisitRepo:            visitRepo,
		VisitQueueSize:       viper.GetInt("stats.queue_size"),
		VisitWorkerCount:     viper.GetInt("stats.worker_count"),
		VisitBatchSize:       viper.GetInt("stats.batch_size"),
		VisitFlushInterval:   viper.GetDuration("stats.flush_interval"),
		IPHashSalt:           viper.GetString("stats.ip_hash_salt"),
		IDAllocator:          allocator,
		CreateQueueSize:      viper.GetInt("write_buffer.queue_size"),
		CreateBatchSize:      viper.GetInt("write_buffer.batch_size"),
		CreateFlushInterval:  viper.GetDuration("write_buffer.flush_interval"),
		CreateEnqueueTimeout: viper.GetDuration("write_buffer.enqueue_timeout"),
		BloomFilter:          bloomFilter,
	}) // 业务逻辑层

	//后台定时任务（自动清理 + 布隆过滤器重建）
	//看看 repo 有没有这两个维护功能： 扫描所有在用的短链接 软删除过期的短链接（通过断言实现）
	if maintenanceRepo, ok := repo.(interface {
		ScanActiveShortCodes(context.Context, int, func([]string) error) error
		SoftDeleteExpired(context.Context, time.Time, int) ([]string, error)
	}); ok {
		// 启动第一个后台任务：过期清理器
		job.StartExpiredCleaner(ctx, maintenanceRepo, shortURLCache, job.ExpiredCleanerOptions{
			Interval:  viper.GetDuration("cleanup.interval"),
			BatchSize: viper.GetInt("cleanup.batch_size"),
		})

		//启动第二个后台任务：布隆过滤器重建
		if redisClient != nil && viper.GetBool("bloom.enabled") {
			job.StartBloomRebuilder(ctx, maintenanceRepo, bloomFilter, job.BloomRebuilderOptions{
				Interval:  viper.GetDuration("bloom.rebuild_interval"),
				BatchSize: viper.GetInt("bloom.rebuild_batch_size"),
			})
		}
	}

	// —————— 第 5 步：启动 gRPC 服务 ——————
	lis, err := net.Listen("tcp", viper.GetString("grpc.addr"))
	if err != nil {
		slog.Error("端口监听失败", "err", err)
		os.Exit(1)
	}

	//创建 gRPC server
	s := grpc.NewServer()

	//注册短链接 RPC 服务
	//把 rpc/grpc/shortUrl.go 里的 ShortUrlServer 注册进去，而这个 server 内部持有刚才创建的 svc。
	proto.RegisterShortUrlServer(s, grpcserver.NewShortUrlServer(svc))

	//注册健康检查服务
	//让外部可以通过 gRPC health check 判断 RPC 服务是否健康。
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(s, healthServer)

	//可选注册到 etcd
	if viper.GetBool("etcd.enabled") {
		etcdClient, err := discovery.NewEtcdClient(splitCSV(viper.GetString("etcd.endpoints")), viper.GetDuration("etcd.dial_timeout"))
		if err != nil {
			slog.Warn("etcd 客户端初始化失败，跳过服务注册", "err", err)
		} else {
			defer etcdClient.Close()

			//把当前 RPC 服务注册进去
			registrar := discovery.NewRegistrar(etcdClient, viper.GetString("service.name"), viper.GetString("service.addr"), viper.GetInt64("etcd.lease_ttl"))
			if err := registrar.Start(ctx); err != nil {
				slog.Warn("服务注册到 etcd 失败", "err", err)
			}
		}
	}

	slog.Info("gRPC 服务启动中", "addr", viper.GetString("grpc.addr"))

	//优雅关闭
	go func() {
		<-ctx.Done()
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		s.GracefulStop()
	}()

	//正式阻塞运行 gRPC 服务。
	if err := s.Serve(lis); err != nil && ctx.Err() == nil {
		slog.Error("gRPC 服务异常退出", "err", err)
		os.Exit(1)
	}
}

// init 在 main 之前执行，设置配置项的默认值。
// 绑定环境变量
// 即使 config.yaml 中没有 grpc.addr 字段，也能使用默认端口 50051。
func init() {
	viper.SetDefault("grpc.addr", ":50051")
	viper.SetDefault("redis.addr", "127.0.0.1:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("cache.default_ttl", "24h")
	viper.SetDefault("cache.not_found_ttl", "1m")
	viper.SetDefault("cache.local_ttl", "5m")
	viper.SetDefault("cache.local_max_entries", 10000)
	viper.SetDefault("cache.jitter_ratio", 0.1)
	viper.SetDefault("stats.queue_size", 10000)
	viper.SetDefault("stats.worker_count", 2)
	viper.SetDefault("stats.batch_size", 100)
	viper.SetDefault("stats.flush_interval", "1s")
	viper.SetDefault("stats.ip_hash_salt", "short_url_dev_salt")
	viper.SetDefault("sharding.count", 64)
	viper.SetDefault("id_allocator.step", 1000)
	viper.SetDefault("write_buffer.queue_size", 10000)
	viper.SetDefault("write_buffer.batch_size", 128)
	viper.SetDefault("write_buffer.flush_interval", "10ms")
	viper.SetDefault("write_buffer.enqueue_timeout", "200ms")
	viper.SetDefault("bloom.enabled", true)
	viper.SetDefault("bloom.key", "short_url:bloom:active")
	viper.SetDefault("bloom.bits", uint64(1<<28))
	viper.SetDefault("bloom.hashes", 7)
	viper.SetDefault("bloom.rebuild_interval", "10m")
	viper.SetDefault("bloom.rebuild_batch_size", 1000)
	viper.SetDefault("cleanup.interval", "1m")
	viper.SetDefault("cleanup.batch_size", 500)
	viper.SetDefault("etcd.enabled", false)
	viper.SetDefault("etcd.endpoints", "127.0.0.1:2379")
	viper.SetDefault("etcd.dial_timeout", "3s")
	viper.SetDefault("etcd.lease_ttl", 10)
	viper.SetDefault("service.name", "short-url-rpc")
	viper.SetDefault("service.addr", "127.0.0.1:50051")
	viper.SetDefault("logger.level", "info")
	viper.SetDefault("logger.development", false)

	_ = viper.BindEnv("db.dsn", "DB_DSN")
	_ = viper.BindEnv("redis.addr", "REDIS_ADDR")
	_ = viper.BindEnv("redis.password", "REDIS_PASSWORD")
	_ = viper.BindEnv("redis.db", "REDIS_DB")
	_ = viper.BindEnv("cache.default_ttl", "CACHE_DEFAULT_TTL")
	_ = viper.BindEnv("cache.not_found_ttl", "CACHE_NOT_FOUND_TTL")
	_ = viper.BindEnv("cache.local_ttl", "CACHE_LOCAL_TTL")
	_ = viper.BindEnv("cache.local_max_entries", "CACHE_LOCAL_MAX_ENTRIES")
	_ = viper.BindEnv("cache.jitter_ratio", "CACHE_JITTER_RATIO")
	_ = viper.BindEnv("stats.queue_size", "STATS_QUEUE_SIZE")
	_ = viper.BindEnv("stats.worker_count", "STATS_WORKER_COUNT")
	_ = viper.BindEnv("stats.batch_size", "STATS_BATCH_SIZE")
	_ = viper.BindEnv("stats.flush_interval", "STATS_FLUSH_INTERVAL")
	_ = viper.BindEnv("stats.ip_hash_salt", "STATS_IP_HASH_SALT")
	_ = viper.BindEnv("sharding.count", "SHARDING_COUNT")
	_ = viper.BindEnv("id_allocator.step", "ID_ALLOCATOR_STEP")
	_ = viper.BindEnv("write_buffer.queue_size", "WRITE_BUFFER_QUEUE_SIZE")
	_ = viper.BindEnv("write_buffer.batch_size", "WRITE_BUFFER_BATCH_SIZE")
	_ = viper.BindEnv("write_buffer.flush_interval", "WRITE_BUFFER_FLUSH_INTERVAL")
	_ = viper.BindEnv("write_buffer.enqueue_timeout", "WRITE_BUFFER_ENQUEUE_TIMEOUT")
	_ = viper.BindEnv("bloom.enabled", "BLOOM_ENABLED")
	_ = viper.BindEnv("bloom.key", "BLOOM_KEY")
	_ = viper.BindEnv("bloom.bits", "BLOOM_BITS")
	_ = viper.BindEnv("bloom.hashes", "BLOOM_HASHES")
	_ = viper.BindEnv("bloom.rebuild_interval", "BLOOM_REBUILD_INTERVAL")
	_ = viper.BindEnv("bloom.rebuild_batch_size", "BLOOM_REBUILD_BATCH_SIZE")
	_ = viper.BindEnv("cleanup.interval", "CLEANUP_INTERVAL")
	_ = viper.BindEnv("cleanup.batch_size", "CLEANUP_BATCH_SIZE")
	_ = viper.BindEnv("etcd.enabled", "ETCD_ENABLED")
	_ = viper.BindEnv("etcd.endpoints", "ETCD_ENDPOINTS")
	_ = viper.BindEnv("etcd.dial_timeout", "ETCD_DIAL_TIMEOUT")
	_ = viper.BindEnv("etcd.lease_ttl", "ETCD_LEASE_TTL")
	_ = viper.BindEnv("service.name", "SERVICE_NAME")
	_ = viper.BindEnv("service.addr", "SERVICE_ADDR")
	_ = viper.BindEnv("logger.level", "LOGGER_LEVEL")
	_ = viper.BindEnv("logger.development", "LOGGER_DEVELOPMENT")
}

func initShortURLCache() (cachepkg.ShortUrlCache, *redis.Client) {
	client := redis.NewClient(&redis.Options{
		Addr:         viper.GetString("redis.addr"),
		Password:     viper.GetString("redis.password"),
		DB:           viper.GetInt("redis.db"),
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis 连接失败，短链接缓存降级为 Noop", "addr", viper.GetString("redis.addr"), "err", err)
		_ = client.Close()
		return cachepkg.NewNoopShortUrlCache(), nil
	}
	slog.Info("Redis 连接成功", "addr", viper.GetString("redis.addr"))
	return cachepkg.NewRedisShortUrlCache(client), client
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
