package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/redis/go-redis/v9"
	clientv3 "go.etcd.io/etcd/client/v3"
	_ "google.golang.org/grpc/balancer/roundrobin"

	"short_url/internal/config"
	"short_url/internal/platform/discovery"
	"short_url/internal/platform/logging"
	"short_url/internal/platform/ratelimit"
	"short_url/internal/transport/httpserver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Web 服务退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	defer logging.Sync()

	cfg, err := config.LoadWeb(os.Getenv("SHORT_URL_WEB_CONFIG"))
	if err != nil {
		return err
	}
	if _, err := logging.Init(cfg.Logger.Level, cfg.Logger.Development); err != nil {
		slog.Warn("按配置初始化日志失败，继续使用默认日志", "err", err)
	}
	configureHystrix(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	grpcTarget := cfg.GRPC.Addr
	var etcdClient *clientv3.Client
	if cfg.Etcd.Enabled {
		client, err := discovery.NewEtcdClient(cfg.Etcd.Endpoints, cfg.Etcd.DialTimeout)
		if err != nil {
			slog.Warn("etcd 客户端初始化失败，回退固定 gRPC 地址", "err", err)
		} else {
			etcdClient = client
			defer etcdClient.Close()
			discovery.RegisterResolver(etcdClient)
			grpcTarget = fmt.Sprintf("%s:///%s", discovery.Scheme, cfg.Service.Name)
		}
	}

	handler, err := httpserver.NewHandler(grpcTarget, cfg.ShortURL.BaseURL)
	if err != nil {
		return fmt.Errorf("创建 gRPC 客户端: %w", err)
	}
	defer handler.Close()

	apiLimiter, redirectLimiter, closeRateLimiters := initRateLimiters(cfg)
	defer closeRateLimiters()
	router := httpserver.NewRouter(handler, httpserver.RouterOptions{
		APILimiter:      apiLimiter,
		RedirectLimiter: redirectLimiter,
		AuthEnabled:     cfg.Auth.Enabled,
		APIKey:          cfg.Auth.APIKey,
	})
	server := &http.Server{Addr: cfg.HTTP.Addr, Handler: router}
	go shutdownHTTPServer(ctx, server)

	slog.Info("HTTP 服务启动中", "addr", cfg.HTTP.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("运行 HTTP 服务: %w", err)
	}
	return nil
}

func configureHystrix(cfg config.Web) {
	commandConfig := hystrix.CommandConfig{
		Timeout:                cfg.CircuitBreaker.TimeoutMS,
		MaxConcurrentRequests:  cfg.CircuitBreaker.MaxConcurrent,
		RequestVolumeThreshold: cfg.CircuitBreaker.RequestVolumeThreshold,
		SleepWindow:            cfg.CircuitBreaker.SleepWindowMS,
		ErrorPercentThreshold:  cfg.CircuitBreaker.ErrorPercentThreshold,
	}
	for _, name := range []string{"short_url_create", "short_url_get", "short_url_stats", "short_url_delete", "short_url_redirect"} {
		hystrix.ConfigureCommand(name, commandConfig)
	}
}

func initRateLimiters(cfg config.Web) (ratelimit.TokenBucketLimiter, ratelimit.TokenBucketLimiter, func()) {
	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr,
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis 限流不可用，降级为内存令牌桶", "err", err)
		_ = client.Close()
		return ratelimit.NewMemoryTokenBucketLimiter(cfg.RateLimit.APIRate, cfg.RateLimit.APIBurst),
			ratelimit.NewMemoryTokenBucketLimiter(cfg.RateLimit.RedirectRate, cfg.RateLimit.RedirectBurst),
			func() {}
	}
	return ratelimit.NewRedisTokenBucketLimiter(client, "short_url:rate_limit:api:", cfg.RateLimit.APIRate, cfg.RateLimit.APIBurst),
		ratelimit.NewRedisTokenBucketLimiter(client, "short_url:rate_limit:redirect:", cfg.RateLimit.RedirectRate, cfg.RateLimit.RedirectBurst),
		func() {
			if err := client.Close(); err != nil {
				slog.Warn("关闭 Redis 限流连接失败", "err", err)
			}
		}
}

func shutdownHTTPServer(ctx context.Context, server *http.Server) {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Warn("HTTP 服务优雅关闭失败", "err", err)
	}
}
