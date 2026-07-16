package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
	clientv3 "go.etcd.io/etcd/client/v3"
	_ "google.golang.org/grpc/balancer/roundrobin"

	"short_url/pkg/discovery"
	"short_url/pkg/logging"
	"short_url/web/middlewares"
	webpkg "short_url/web/pkg"
	"short_url/web/routes"
)

func main() {

	//最后一步强制把所有没写到磁盘的日志全部刷出去，一条都不丢。
	defer logging.Sync()

	//创建一个能监听系统退出信号的上下文 ctx
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// —————— 第 1 步：加载配置 ——————
	// 配置搜索路径与 RPC 服务采用相同策略：
	// 本地开发从 ./web/config/ 读取，Docker 容器从 /etc/short_url/ 读取。

	//告诉 Viper 配置文件名
	viper.SetConfigName("config")

	//告诉 Viper 配置格式是 YAML。
	viper.SetConfigType("yaml")

	//没有配置文件时，并且环境变量也没有时，用默认值
	viper.SetDefault("short_url.base_url", "http://localhost:8080")

	//把 Viper 里的配置 key 和系统环境变量绑定在一起
	//优先级：环境变量>配置文件>默认值
	_ = viper.BindEnv("http.addr", "HTTP_ADDR")
	_ = viper.BindEnv("grpc.addr", "GRPC_ADDR")
	_ = viper.BindEnv("short_url.base_url", "SHORT_URL_BASE_URL")

	//配置搜索路径：
	//意思是本地开发可以放在 web/config/config.yaml，Docker 里可以放到 /etc/short_url/config.yaml
	viper.AddConfigPath("./web/config")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/short_url")

	//读取配置。
	if err := viper.ReadInConfig(); err != nil {
		slog.Error("读取配置文件失败", "err", err)
		os.Exit(1)
	}

	//初始化日志
	if _, err := logging.Init(viper.GetString("logger.level"), viper.GetBool("logger.development")); err != nil {
		slog.Warn("按配置初始化日志失败，继续使用默认日志", "err", err)
	}

	//配置 Hystrix 熔断器
	configureHystrix()

	// —————— 第 2 步：连接 gRPC 后端 ——————
	// Web 服务本身不直接访问数据库，所有数据操作通过 gRPC 调用 RPC 服务完成。
	// 这样实现了"关注点分离"：Web 层只负责 HTTP 协议处理，RPC 层负责业务逻辑和数据访问。

	//先默认从配置里拿 RPC 地址。
	grpcTarget := viper.GetString("grpc.addr")

	//声明一个 etcd 客户端变量。先不创建，只有启用 etcd 时才创建。
	var etcdClient *clientv3.Client
	if viper.GetBool("etcd.enabled") {
		//连接 etcd。
		client, err := discovery.NewEtcdClient(splitCSV(viper.GetString("etcd.endpoints")), viper.GetDuration("etcd.dial_timeout"))
		if err != nil {
			slog.Warn("etcd 客户端初始化失败，回退固定 gRPC 地址", "err", err)
		} else {
			etcdClient = client
			defer etcdClient.Close()
			discovery.RegisterResolver(etcdClient)

			//grpcTarget 从普通地址变成服务发现地址。
			grpcTarget = fmt.Sprintf("%s:///%s", discovery.Scheme, viper.GetString("service.name"))
		}
	}

	//会创建 Web 层 handler，并在里面创建 gRPC client。
	handler, err := routes.NewHandler(grpcTarget, viper.GetString("short_url.base_url"))
	if err != nil {
		//直接退出
		slog.Error("gRPC 客户端创建失败", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := handler.Close(); err != nil {
			slog.Warn("关闭 gRPC 客户端连接失败", "err", err)
		}
	}()

	// —————— 第 3 步：注册路由并启动 HTTP 服务 ——————
	// gin.Default() 会自动带上 Logger 和 Recovery 中间件：
	// - Logger：记录每个 HTTP 请求的耗时、状态码
	// - Recovery：捕获 handler 中的 panic，返回 500 而不是让进程崩溃

	//创建两个限流器
	//apiLimiter：限制 /api/... 管理接口。
	//redirectLimiter：限制短链跳转接口。
	apiLimiter, redirectLimiter, closeRateLimiters := initRateLimiters()
	defer closeRateLimiters()
	r := gin.New()

	//如果某个 handler panic，不让整个进程崩掉，而是返回 500，给每个请求加一个 X-Request-ID，方便查日志
	//记录访问日志，比如请求路径、状态码、耗时、IP。
	r.Use(gin.Recovery(), middlewares.RequestID(), middlewares.AccessLog())

	//注册限流中间件
	//  /api/... 使用 apiLimiter
	//其他路径使用 redirectLimiter
	r.Use(middlewares.RateLimit(apiLimiter, redirectLimiter))

	//注册 API Key 鉴权中间件
	//这个只保护 /api/ 开头的接口。
	r.Use(middlewares.APIKey(viper.GetBool("auth.enabled"), viper.GetString("auth.api_key")))
	//服务是否活着。
	r.GET("/healthz", handler.Healthz)

	//服务是否准备好接流量，通常会检查后端 RPC 状态。
	r.GET("/readyz", handler.Readyz)
	r.POST("/api/short-links", handler.CreateShortLink)              // 创建短链接
	r.GET("/api/short-links/:code", handler.GetShortLink)            // 查询短链接详情
	r.GET("/api/short-links/:code/stats", handler.GetShortLinkStats) // 查询访问统计
	r.DELETE("/api/short-links/:code", handler.DeleteShortLink)      // 软删除短链接

	//如果没有匹配到上面的固定路由，就进入短链跳转逻辑。Gin 发现没有显式注册 /000001，于是进入 NoRoute，再由 handler.Redirect 解析短码并跳转。
	r.NoRoute(handler.Redirect) // 短码重定向

	slog.Info("HTTP 服务启动中", "addr", viper.GetString("http.addr"))

	//创建 HTTP Server
	server := &http.Server{
		Addr:    viper.GetString("http.addr"),
		Handler: r,
	}

	//等着程序退出信号
	go func() {
		<-ctx.Done()

		//5 秒时间优雅关闭
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		//不再接收新请求，但尽量让已经进来的请求处理完。
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Warn("HTTP 服务优雅关闭失败", "err", err)
		}
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP 服务异常退出", "err", err)
		os.Exit(1)
	}
}

// 设置默认配置
// 绑定环境变量
func init() {
	viper.SetDefault("http.addr", ":8080")
	viper.SetDefault("grpc.addr", "127.0.0.1:50051")
	viper.SetDefault("short_url.base_url", "http://localhost:8080")
	viper.SetDefault("redis.addr", "127.0.0.1:6379")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("rate_limit.api_rate", 50.0)
	viper.SetDefault("rate_limit.api_burst", 100)
	viper.SetDefault("rate_limit.redirect_rate", 500.0)
	viper.SetDefault("rate_limit.redirect_burst", 1000)
	viper.SetDefault("auth.enabled", false)
	viper.SetDefault("auth.api_key", "")
	viper.SetDefault("etcd.enabled", false)
	viper.SetDefault("etcd.endpoints", "127.0.0.1:2379")
	viper.SetDefault("etcd.dial_timeout", "3s")
	viper.SetDefault("service.name", "short-url-rpc")
	viper.SetDefault("logger.level", "info")
	viper.SetDefault("logger.development", false)
	viper.SetDefault("circuit_breaker.timeout_ms", 2500)
	viper.SetDefault("circuit_breaker.max_concurrent", 100)
	viper.SetDefault("circuit_breaker.request_volume_threshold", 20)
	viper.SetDefault("circuit_breaker.sleep_window_ms", 5000)
	viper.SetDefault("circuit_breaker.error_percent_threshold", 50)

	_ = viper.BindEnv("redis.addr", "REDIS_ADDR")
	_ = viper.BindEnv("redis.password", "REDIS_PASSWORD")
	_ = viper.BindEnv("redis.db", "REDIS_DB")
	_ = viper.BindEnv("rate_limit.api_rate", "RATE_LIMIT_API_RATE")
	_ = viper.BindEnv("rate_limit.api_burst", "RATE_LIMIT_API_BURST")
	_ = viper.BindEnv("rate_limit.redirect_rate", "RATE_LIMIT_REDIRECT_RATE")
	_ = viper.BindEnv("rate_limit.redirect_burst", "RATE_LIMIT_REDIRECT_BURST")
	_ = viper.BindEnv("auth.enabled", "AUTH_ENABLED")
	_ = viper.BindEnv("auth.api_key", "AUTH_API_KEY")
	_ = viper.BindEnv("etcd.enabled", "ETCD_ENABLED")
	_ = viper.BindEnv("etcd.endpoints", "ETCD_ENDPOINTS")
	_ = viper.BindEnv("etcd.dial_timeout", "ETCD_DIAL_TIMEOUT")
	_ = viper.BindEnv("service.name", "SERVICE_NAME")
	_ = viper.BindEnv("logger.level", "LOGGER_LEVEL")
	_ = viper.BindEnv("logger.development", "LOGGER_DEVELOPMENT")
	_ = viper.BindEnv("circuit_breaker.timeout_ms", "CIRCUIT_BREAKER_TIMEOUT_MS")
	_ = viper.BindEnv("circuit_breaker.max_concurrent", "CIRCUIT_BREAKER_MAX_CONCURRENT")
	_ = viper.BindEnv("circuit_breaker.request_volume_threshold", "CIRCUIT_BREAKER_REQUEST_VOLUME_THRESHOLD")
	_ = viper.BindEnv("circuit_breaker.sleep_window_ms", "CIRCUIT_BREAKER_SLEEP_WINDOW_MS")
	_ = viper.BindEnv("circuit_breaker.error_percent_threshold", "CIRCUIT_BREAKER_ERROR_PERCENT_THRESHOLD")
}

// 配置熔断器。
func configureHystrix() {
	config := hystrix.CommandConfig{
		Timeout:                viper.GetInt("circuit_breaker.timeout_ms"),               //RPC 调用最多等多久。
		MaxConcurrentRequests:  viper.GetInt("circuit_breaker.max_concurrent"),           //最多允许多少并发 RPC 调用。
		RequestVolumeThreshold: viper.GetInt("circuit_breaker.request_volume_threshold"), //至少多少请求后才开始判断错误率。
		SleepWindow:            viper.GetInt("circuit_breaker.sleep_window_ms"),          //熔断后多久尝试恢复。
		ErrorPercentThreshold:  viper.GetInt("circuit_breaker.error_percent_threshold"),  //错误率达到多少就熔断
	}

	//给每种 RPC 操作都套上同一套熔断配置。
	for _, name := range []string{"short_url_create", "short_url_get", "short_url_stats", "short_url_delete", "short_url_redirect"} {
		hystrix.ConfigureCommand(name, config)
	}
}

// 返回API 限流器
// 跳转限流器
func initRateLimiters() (webpkg.TokenBucketLimiter, webpkg.TokenBucketLimiter, func()) {
	//先尝试连接 Redis：
	client := redis.NewClient(&redis.Options{
		Addr:         viper.GetString("redis.addr"),
		Password:     viper.GetString("redis.password"),
		DB:           viper.GetInt("redis.db"),
		DialTimeout:  time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	//然后 ping 一下：
	//如果 Redis 不可用，就降级成内存限流：
	if err := client.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis 限流不可用，降级为内存令牌桶", "err", err)
		_ = client.Close()
		return webpkg.NewMemoryTokenBucketLimiter(viper.GetFloat64("rate_limit.api_rate"), viper.GetInt("rate_limit.api_burst")),
			webpkg.NewMemoryTokenBucketLimiter(viper.GetFloat64("rate_limit.redirect_rate"), viper.GetInt("rate_limit.redirect_burst")),
			func() {}
	}
	return webpkg.NewRedisTokenBucketLimiter(client, "short_url:rate_limit:api:", viper.GetFloat64("rate_limit.api_rate"), viper.GetInt("rate_limit.api_burst")),
		webpkg.NewRedisTokenBucketLimiter(client, "short_url:rate_limit:redirect:", viper.GetFloat64("rate_limit.redirect_rate"), viper.GetInt("rate_limit.redirect_burst")),
		func() {
			if err := client.Close(); err != nil {
				slog.Warn("关闭 Redis 限流连接失败", "err", err)
			}
		}
}

// 逗号分隔的字符串切成数组。
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
