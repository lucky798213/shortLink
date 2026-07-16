package main

import (
	"testing"

	"github.com/gin-gonic/gin"

	"short_url/internal/transport/httpserver"
)

func TestRouteRegistration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := httpserver.NewHandler("127.0.0.1:1", "http://localhost:8080")
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	router := httpserver.NewRouter(handler, httpserver.RouterOptions{})
	want := map[string]bool{
		"GET /healthz":                     false,
		"GET /readyz":                      false,
		"POST /api/short-links":            false,
		"GET /api/short-links/:code":       false,
		"GET /api/short-links/:code/stats": false,
		"DELETE /api/short-links/:code":    false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, registered := range want {
		if !registered {
			t.Errorf("route %s is not registered", route)
		}
	}
}
