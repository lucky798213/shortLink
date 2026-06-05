package routes

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"short_url/proto"
	"short_url/web/middlewares"
)

const (
	statusActive   = "active"
	statusExpired  = "expired"
	statusNotFound = "not_found"
)

// Handler 封装 HTTP 路由处理所需的依赖。
// 将 gRPC 客户端作为 Handler 的成员变量，避免在每个路由处理函数中重复创建连接。
type Handler struct {
	rpcClient    proto.ShortUrlClient
	healthClient grpc_health_v1.HealthClient
	baseURL      string
}

// NewHandler 创建 Handler 并建立到 RPC 服务的 gRPC 连接。
//
// grpc.WithTransportCredentials(insecure.NewCredentials()) 说明：
// 当前 MVP 阶段 RPC 服务没有配置 TLS，使用明文传输。
// 生产环境应该替换为 TLS 证书认证。
func NewHandler(grpcAddr string, baseURL string) (*Handler, error) {
	//创建 grpc conn，
	conn, err := grpc.NewClient(grpcAddr,
		// 设置传输层凭证，凭证类型：明文不加密
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)

	if err != nil {
		return nil, err
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &Handler{
		rpcClient:    proto.NewShortUrlClient(conn),
		healthClient: grpc_health_v1.NewHealthClient(conn),
		baseURL:      baseURL,
	}, nil
}

// CreateShortLink 处理 POST /api/short-links 请求。
//
// 请求体格式：{"origin_url": "https://example.com/very/long/url", "expire_at": 0}
// 成功响应：  {"short_code": "000001", "short_url": "http://localhost:8080/000001", ...}
// 失败响应：  {"error": "..."}
//
// 超时设置 3 秒的原因：
// 创建短链接需要一次 INSERT + 一次 UPDATE，正常情况下在几十毫秒内完成。
// 3 秒超时既给了数据库异常重试的余量，又不会让客户端等太久。
func (h *Handler) CreateShortLink(c *gin.Context) {
	var req struct {
		OriginURL string `json:"origin_url" binding:"required"`
		ExpireAt  int64  `json:"expire_at"`
	}

	// ShouldBindJSON 会校验 JSON 格式和 required 约束，
	// 如果 origin_url 字段缺失或不是字符串，返回 400 错误。
	if err := c.ShouldBindJSON(&req); err != nil {
		middlewares.JSONError(c, http.StatusBadRequest, "bad_request", "origin_url is required")
		return
	}

	//校验传入的链接
	originURL, err := normalizeOriginURL(req.OriginURL)
	if err != nil {
		middlewares.JSONError(c, http.StatusBadRequest, "bad_request", "origin_url must be a valid http or https URL")
		return
	}

	//校验这个过期时间合不合法
	if err := validateExpireAt(req.ExpireAt); err != nil {
		middlewares.JSONError(c, http.StatusBadRequest, "bad_request", "expire_at must be a future Unix timestamp or 0")
		return
	}

	// 使用 context.WithTimeout 设置超时，防止 RPC 调用一直阻塞。
	// 基于 c.Request.Context() 创建子 context，这样客户端断开连接时也会自动取消。
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	//调用 rpc 来生成短链接
	var resp *proto.CreateShortUrlResponse
	err = hystrix.DoC(ctx, "short_url_create", func(ctx context.Context) error {
		var callErr error
		resp, callErr = h.rpcClient.CreateShortUrl(ctx, &proto.CreateShortUrlRequest{
			OriginUrl: originURL,
			ExpireAt:  req.ExpireAt,
		})
		return callErr
	}, nil)

	if err != nil {
		slog.Error("RPC CreateShortUrl 调用失败", "err", err)
		middlewares.JSONError(c, http.StatusServiceUnavailable, "rpc_unavailable", "failed to create short url")
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"short_code": resp.ShortCode,
		"short_url":  h.shortURL(resp.ShortCode),
		"origin_url": originURL,
		"expire_at":  req.ExpireAt,
	})
}

// GetShortLink 处理 GET /api/short-links/:code 请求。
func (h *Handler) GetShortLink(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	//在 RPC 服务中寻找短链接对应的原始链接
	var resp *proto.GetShortUrlResponse
	err := hystrix.DoC(ctx, "short_url_get", func(ctx context.Context) error {
		var callErr error
		resp, callErr = h.rpcClient.GetShortUrl(ctx, &proto.GetShortUrlRequest{
			ShortCode: code,
		})
		return callErr
	}, nil)

	if err != nil {
		slog.Error("RPC GetShortUrl 调用失败", "err", err)
		middlewares.JSONError(c, http.StatusServiceUnavailable, "rpc_unavailable", "internal error")
		return
	}

	if resp.Status == statusNotFound || resp.ShortCode == "" {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "short url not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"short_code": resp.ShortCode,
		"short_url":  h.shortURL(resp.ShortCode),
		"origin_url": resp.OriginUrl,
		"created_at": resp.CreatedAt,
		"expire_at":  resp.ExpireAt,
		"status":     resp.Status,
	})
}

// GetShortLinkStats 处理 GET /api/short-links/:code/stats 请求。
// // 基于 c.Request.Context() 创建子 context，这样客户端断开连接时也会自动取消。
func (h *Handler) GetShortLinkStats(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	//通过 RPC 获取访问数据
	var resp *proto.GetShortUrlStatsResponse
	err := hystrix.DoC(ctx, "short_url_stats", func(ctx context.Context) error {
		var callErr error
		resp, callErr = h.rpcClient.GetShortUrlStats(ctx, &proto.GetShortUrlStatsRequest{
			ShortCode: code,
		})
		return callErr
	}, nil)
	if err != nil {
		slog.Error("RPC GetShortUrlStats 调用失败", "err", err)
		middlewares.JSONError(c, http.StatusServiceUnavailable, "rpc_unavailable", "internal error")
		return
	}
	if resp.Status == statusNotFound || resp.ShortCode == "" {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "short url not found")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"short_code":      resp.ShortCode,
		"pv":              resp.Pv, //访问量（短链接被打开 / 点击了多少次）
		"uv":              resp.Uv, //访客数 （独立访客数（一个人算一次））
		"last_visited_at": resp.LastVisitedAt,
		"top_referers":    statsItems(resp.TopReferers),
		"top_user_agents": statsItems(resp.TopUserAgents),
	})
}

// DeleteShortLink 处理 DELETE /api/short-links/:code 请求。
func (h *Handler) DeleteShortLink(c *gin.Context) {
	code := c.Param("code")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	var resp *proto.DeleteShortUrlResponse
	err := hystrix.DoC(ctx, "short_url_delete", func(ctx context.Context) error {
		var callErr error
		resp, callErr = h.rpcClient.DeleteShortUrl(ctx, &proto.DeleteShortUrlRequest{
			ShortCode: code,
		})
		return callErr
	}, nil)
	if err != nil {
		slog.Error("RPC DeleteShortUrl 调用失败", "err", err)
		middlewares.JSONError(c, http.StatusServiceUnavailable, "rpc_unavailable", "internal error")
		return
	}
	if !resp.Deleted {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "short url not found")
		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

// Redirect 处理 GET /:code 请求，将短码重定向到原始链接。
//
// 重定向类型使用 301 (Moved Permanently) 而不是 302 (Found) 的原因：
// - 短链接的映射关系是永久的，不会变化
// - 301 能让浏览器缓存重定向结果，下次访问同一短码时直接跳转，减少服务器压力
// - 302 每次都会请求服务器，对短链接场景来说是不必要的开销
//
// 短码不存在时返回 404 的原因：
// 给用户明确的"此链接不存在或已失效"的提示，而非静默失败。
func (h *Handler) Redirect(c *gin.Context) {
	if c.Request.Method != http.MethodGet {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "not found")
		return
	}

	code := h.redirectShortCode(c) // 从 URL 路径中提取短码，如 /000001 → code="000001"
	if code == "" {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "short url not found")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	var resp *proto.GetOriginUrlResponse
	err := hystrix.DoC(ctx, "short_url_redirect", func(ctx context.Context) error {
		var callErr error
		resp, callErr = h.rpcClient.GetOriginUrl(ctx, &proto.GetOriginUrlRequest{
			ShortCode: code,
			ClientIp:  c.ClientIP(),
			UserAgent: c.GetHeader("User-Agent"),
			Referer:   c.GetHeader("Referer"),
		})
		return callErr
	}, nil)
	if err != nil {
		slog.Error("RPC GetOriginUrl 调用失败", "err", err)
		middlewares.JSONError(c, http.StatusServiceUnavailable, "rpc_unavailable", "internal error")
		return
	}
	if resp.Status == statusExpired {
		middlewares.JSONError(c, http.StatusGone, "expired", "short url expired")
		return
	}
	// OriginUrl 为空表示短码不存在（或被软删除）
	if resp.Status == statusNotFound || resp.OriginUrl == "" {
		middlewares.JSONError(c, http.StatusNotFound, "not_found", "short url not found")
		return
	}

	// 301 永久重定向到原始链接
	c.Redirect(http.StatusMovedPermanently, resp.OriginUrl)
}

// 严格校验传入的「源站地址（Origin URL）」是否为合法的 HTTP/HTTPS 地址
func normalizeOriginURL(rawURL string) (string, error) {
	originURL := strings.TrimSpace(rawURL)
	if originURL == "" {
		return "", url.InvalidHostError("empty origin_url")
	}

	parsed, err := url.Parse(originURL)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", url.InvalidHostError("missing host")
	}
	if strings.ContainsAny(parsed.Host, " \t\r\n") {
		return "", url.InvalidHostError("host contains whitespace")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", url.InvalidHostError("unsupported scheme")
	}
	return originURL, nil
}

func validateExpireAt(expireAt int64) error {
	// 如果 expireAt 不是0，并且 小于等于 当前时间
	if expireAt != 0 && expireAt <= time.Now().Unix() {
		return url.InvalidHostError("expire_at must be in the future")
	}
	return nil
}

func (h *Handler) shortURL(shortCode string) string {
	return h.baseURL + "/" + shortCode
}

func (h *Handler) redirectShortCode(c *gin.Context) string {
	if code := c.Param("code"); code != "" {
		return code
	}
	path := strings.Trim(c.Request.URL.Path, "/")
	if path == "" || strings.Contains(path, "/") || strings.HasPrefix(path, "api/") {
		return ""
	}
	return path
}

func statsItems(items []*proto.StatsItem) []gin.H {
	resp := make([]gin.H, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		resp = append(resp, gin.H{
			"value": item.Value,
			"count": item.Count,
		})
	}
	return resp
}
