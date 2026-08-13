package httpserver

import (
	"github.com/gin-gonic/gin"

	"short_url/internal/platform/ratelimit"
	"short_url/internal/transport/httpserver/middleware"
)

type RouterOptions struct {
	APILimiter      ratelimit.TokenBucketLimiter
	RedirectLimiter ratelimit.TokenBucketLimiter
	AuthEnabled     bool
	APIKey          string
}

func NewRouter(handler *Handler, opts RouterOptions) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), middleware.RequestID(), middleware.AccessLog())
	router.Use(middleware.RateLimit(opts.APILimiter, opts.RedirectLimiter))
	router.Use(middleware.APIKey(opts.AuthEnabled, opts.APIKey))
	registerFrontendRoutes(router)
	router.GET("/healthz", handler.Healthz)
	router.GET("/readyz", handler.Readyz)
	router.POST("/api/short-links", handler.CreateShortLink)
	router.GET("/api/short-links/:code", handler.GetShortLink)
	router.GET("/api/short-links/:code/stats", handler.GetShortLinkStats)
	router.DELETE("/api/short-links/:code", handler.DeleteShortLink)
	router.NoRoute(handler.Redirect)
	return router
}
