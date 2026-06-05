package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/health/grpc_health_v1"

	"short_url/web/middlewares"
)

func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) Readyz(c *gin.Context) {
	if h.healthClient == nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	resp, err := h.healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		middlewares.JSONError(c, http.StatusServiceUnavailable, "not_ready", "rpc health check failed")
		return
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		middlewares.JSONError(c, http.StatusServiceUnavailable, "not_ready", "rpc not serving")
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
