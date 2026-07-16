package httpserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/afex/hystrix-go/hystrix"
	"github.com/gin-gonic/gin"

	proto "short_url/api/shortlink/v1"
)

func BenchmarkRedirectHandlerMockRPC(b *testing.B) {
	gin.SetMode(gin.TestMode)
	configureBenchmarkHystrix()
	handler := &Handler{
		rpcClient: &fakeShortUrlClient{
			getOriginResp: &proto.GetOriginUrlResponse{
				OriginUrl: "https://example.com",
				Status:    statusActive,
			},
		},
		baseURL: "http://localhost:8080",
	}
	router := gin.New()
	router.GET("/:code", handler.Redirect)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/000001", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusMovedPermanently {
			b.Fatalf("status = %d, want %d", w.Code, http.StatusMovedPermanently)
		}
	}
}

func BenchmarkCreateShortLinkHandlerMockRPC(b *testing.B) {
	gin.SetMode(gin.TestMode)
	configureBenchmarkHystrix()
	handler := &Handler{
		rpcClient: &fakeShortUrlClient{},
		baseURL:   "http://localhost:8080",
	}
	router := gin.New()
	router.POST("/api/short-links", handler.CreateShortLink)

	body := []byte(`{"origin_url":"https://example.com/a"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/short-links", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			b.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
		}
	}
}

func configureBenchmarkHystrix() {
	config := hystrix.CommandConfig{
		Timeout:                2500,
		MaxConcurrentRequests:  100000,
		RequestVolumeThreshold: 100000,
		SleepWindow:            5000,
		ErrorPercentThreshold:  50,
	}
	for _, name := range []string{"short_url_create", "short_url_redirect"} {
		hystrix.ConfigureCommand(name, config)
	}
}
