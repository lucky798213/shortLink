package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"short_url/internal/platform/logging"
	"short_url/internal/platform/ratelimit"
)

const RequestIDKey = "request_id"

type ErrorResponse struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func JSONError(c *gin.Context, status int, code string, message string) {
	requestID, _ := c.Get(RequestIDKey)
	resp := ErrorResponse{
		Code:    code,
		Message: message,
	}
	if value, ok := requestID.(string); ok {
		resp.RequestID = value
	}
	c.JSON(status, resp)
}

func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if strings.TrimSpace(requestID) == "" {
			requestID = newRequestID()
		}
		c.Set(RequestIDKey, requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)
		c.Next()
	}
}

func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		requestID, _ := c.Get(RequestIDKey)
		logging.L().Info("http request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
			zap.String("request_id", stringValue(requestID)),
		)
	}
}

// 限流中间件
func RateLimit(managementLimiter ratelimit.TokenBucketLimiter, redirectLimiter ratelimit.TokenBucketLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		limiter := redirectLimiter
		scope := "redirect"

		//根据你这个请求的路径来判断是正常请求还是跳转（重定向）
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			limiter = managementLimiter
			scope = "api"
		}
		if limiter == nil {
			c.Next()
			return
		}
		allowed, err := limiter.Allow(c.Request.Context(), scope+":"+c.ClientIP())
		if err != nil {
			logging.L().Warn("rate limiter failed, allowing request", zap.Error(err))
			c.Next()
			return
		}
		if !allowed {
			JSONError(c, http.StatusTooManyRequests, "rate_limited", "too many requests")
			c.Abort()
			return
		}
		c.Next()
	}
}

func APIKey(enabled bool, apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !enabled || !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		if c.GetHeader("X-API-Key") != apiKey {
			JSONError(c, http.StatusUnauthorized, "unauthorized", "invalid api key")
			c.Abort()
			return
		}
		c.Next()
	}
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf[:])
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
