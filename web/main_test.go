package main

import (
	"testing"

	"github.com/gin-gonic/gin"

	"short_url/web/routes"
)

func TestRouteRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := routes.NewHandler("127.0.0.1:1", "http://localhost:8080")
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}

	r := gin.New()
	r.POST("/api/short-links", handler.CreateShortLink)
	r.GET("/api/short-links/:code", handler.GetShortLink)
	r.GET("/api/short-links/:code/stats", handler.GetShortLinkStats)
	r.DELETE("/api/short-links/:code", handler.DeleteShortLink)
	r.NoRoute(handler.Redirect)
}
