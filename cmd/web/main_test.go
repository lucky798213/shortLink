package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
		"GET /":                            false,
		"GET /assets/styles.css":           false,
		"GET /assets/app.js":               false,
		"GET /assets/favicon.svg":          false,
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

func TestFrontendAssets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := httpserver.NewHandler("127.0.0.1:1", "http://localhost:8080")
	if err != nil {
		t.Fatalf("NewHandler() error: %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	router := httpserver.NewRouter(handler, httpserver.RouterOptions{})
	tests := []struct {
		name         string
		path         string
		contentType  string
		cacheControl string
		bodyContains string
	}{
		{
			name:         "首页",
			path:         "/",
			contentType:  "text/html; charset=utf-8",
			cacheControl: "no-cache",
			bodyContains: "短链接工作台",
		},
		{
			name:         "样式",
			path:         "/assets/styles.css",
			contentType:  "text/css; charset=utf-8",
			cacheControl: "public, max-age=3600",
			bodyContains: "--paper:",
		},
		{
			name:         "脚本",
			path:         "/assets/app.js",
			contentType:  "text/javascript; charset=utf-8",
			cacheControl: "public, max-age=3600",
			bodyContains: "/api/short-links",
		},
		{
			name:         "图标",
			path:         "/assets/favicon.svg",
			contentType:  "image/svg+xml",
			cacheControl: "public, max-age=3600",
			bodyContains: "<svg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if got := w.Header().Get("Cache-Control"); got != tt.cacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.cacheControl)
			}
			if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
				t.Errorf("Content-Security-Policy = %q, want self-only policy", got)
			}
			if !strings.Contains(w.Body.String(), tt.bodyContains) {
				t.Errorf("response body does not contain %q", tt.bodyContains)
			}
		})
	}
}
