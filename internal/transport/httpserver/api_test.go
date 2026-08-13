package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	proto "short_url/api/shortlink/v1"
)

type fakeShortUrlClient struct {
	originURL    string
	expireAt     int64
	createCalled bool
	createErr    error

	getShortResp    *proto.GetShortUrlResponse
	getOriginResp   *proto.GetOriginUrlResponse
	statsResp       *proto.GetShortUrlStatsResponse
	deleteResp      *proto.DeleteShortUrlResponse
	getShortCode    string
	statsShortCode  string
	deleteShortCode string
	originRequest   *proto.GetOriginUrlRequest
}

func (f *fakeShortUrlClient) CreateShortUrl(ctx context.Context, in *proto.CreateShortUrlRequest, opts ...grpc.CallOption) (*proto.CreateShortUrlResponse, error) {
	f.createCalled = true
	f.originURL = in.OriginUrl
	f.expireAt = in.ExpireAt
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &proto.CreateShortUrlResponse{ShortCode: "000001"}, nil
}

func (f *fakeShortUrlClient) GetOriginUrl(ctx context.Context, in *proto.GetOriginUrlRequest, opts ...grpc.CallOption) (*proto.GetOriginUrlResponse, error) {
	f.originRequest = in
	if f.getOriginResp != nil {
		return f.getOriginResp, nil
	}
	return &proto.GetOriginUrlResponse{Status: statusNotFound}, nil
}

func (f *fakeShortUrlClient) GetShortUrl(ctx context.Context, in *proto.GetShortUrlRequest, opts ...grpc.CallOption) (*proto.GetShortUrlResponse, error) {
	f.getShortCode = in.ShortCode
	if f.getShortResp != nil {
		return f.getShortResp, nil
	}
	return &proto.GetShortUrlResponse{Status: statusNotFound}, nil
}

func (f *fakeShortUrlClient) DeleteShortUrl(ctx context.Context, in *proto.DeleteShortUrlRequest, opts ...grpc.CallOption) (*proto.DeleteShortUrlResponse, error) {
	f.deleteShortCode = in.ShortCode
	if f.deleteResp != nil {
		return f.deleteResp, nil
	}
	return &proto.DeleteShortUrlResponse{}, nil
}

func (f *fakeShortUrlClient) GetShortUrlStats(ctx context.Context, in *proto.GetShortUrlStatsRequest, opts ...grpc.CallOption) (*proto.GetShortUrlStatsResponse, error) {
	f.statsShortCode = in.ShortCode
	if f.statsResp != nil {
		return f.statsResp, nil
	}
	return &proto.GetShortUrlStatsResponse{Status: statusNotFound}, nil
}

func TestNormalizeOriginURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{
			name:   "valid http URL",
			rawURL: "http://example.com/path?a=1",
			want:   "http://example.com/path?a=1",
		},
		{
			name:   "valid https URL trims surrounding spaces",
			rawURL: "  https://example.com/a/b  ",
			want:   "https://example.com/a/b",
		},
		{
			name:    "empty URL",
			rawURL:  "",
			wantErr: true,
		},
		{
			name:    "missing scheme",
			rawURL:  "example.com/path",
			wantErr: true,
		},
		{
			name:    "missing host",
			rawURL:  "https:///path",
			wantErr: true,
		},
		{
			name:    "unsupported scheme",
			rawURL:  "ftp://example.com/path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeOriginURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeOriginURL(%q) expected error", tt.rawURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeOriginURL(%q) unexpected error: %v", tt.rawURL, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeOriginURL(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestCreateShortLinkResponseIncludesFullShortURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	body := bytes.NewBufferString(`{"origin_url":"  https://example.com/a  "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/short-links", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.CreateShortLink(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusCreated, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["short_code"] != "000001" {
		t.Fatalf("short_code = %q, want 000001", resp["short_code"])
	}
	if resp["short_url"] != "http://localhost:8080/000001" {
		t.Fatalf("short_url = %q, want http://localhost:8080/000001", resp["short_url"])
	}
	if resp["origin_url"] != "https://example.com/a" {
		t.Fatalf("origin_url = %q, want https://example.com/a", resp["origin_url"])
	}
	if client.originURL != "https://example.com/a" {
		t.Fatalf("RPC origin_url = %q, want https://example.com/a", client.originURL)
	}
	if client.expireAt != 0 {
		t.Fatalf("RPC expire_at = %d, want 0", client.expireAt)
	}
}

func TestCreateShortLinkRejectsInvalidOriginURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	body := bytes.NewBufferString(`{"origin_url":"ftp://example.com/a"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/short-links", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.CreateShortLink(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if client.createCalled {
		t.Fatal("RPC client should not be called for invalid origin_url")
	}
}

func TestCreateShortLinkRejectsPastExpireAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	body := bytes.NewBufferString(`{"origin_url":"https://example.com/a","expire_at":1}`)
	req := httptest.NewRequest(http.MethodPost, "/api/short-links", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.CreateShortLink(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if client.createCalled {
		t.Fatal("RPC client should not be called for expired expire_at")
	}
}

func TestGetShortLinkReturnsDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		getShortResp: &proto.GetShortUrlResponse{
			ShortCode: "000001",
			OriginUrl: "https://example.com/a",
			CreatedAt: 100,
			ExpireAt:  0,
			Status:    statusActive,
		},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/short-links/000001", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "000001"}}
	c.Request = req

	handler.GetShortLink(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["short_code"] != "000001" || resp["short_url"] != "http://localhost:8080/000001" || resp["status"] != shortLinkStatusString(statusActive) {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if client.getShortCode != "000001" {
		t.Fatalf("getShortCode = %q, want 000001", client.getShortCode)
	}
}

func TestDeleteShortLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		deleteResp: &proto.DeleteShortUrlResponse{Deleted: true},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/short-links/000001", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "000001"}}
	c.Request = req

	handler.DeleteShortLink(c)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNoContent, w.Body.String())
	}
	if client.deleteShortCode != "000001" {
		t.Fatalf("deleteShortCode = %q, want 000001", client.deleteShortCode)
	}
}

func TestGetShortLinkStatsReturnsStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		statsResp: &proto.GetShortUrlStatsResponse{
			ShortCode:     "000001",
			Pv:            10,
			Uv:            3,
			LastVisitedAt: 100,
			Status:        statusActive,
			TopReferers: []*proto.StatsItem{
				{Value: "https://example.com", Count: 7},
			},
			TopUserAgents: []*proto.StatsItem{
				{Value: "Mozilla/5.0", Count: 5},
			},
		},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/short-links/000001/stats", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "000001"}}
	c.Request = req

	handler.GetShortLinkStats(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["short_code"] != "000001" || resp["pv"].(float64) != 10 || resp["uv"].(float64) != 3 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if client.statsShortCode != "000001" {
		t.Fatalf("statsShortCode = %q, want 000001", client.statsShortCode)
	}
}

func TestGetShortLinkStatsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		statsResp: &proto.GetShortUrlStatsResponse{Status: statusNotFound},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/short-links/missing/stats", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "missing"}}
	c.Request = req

	handler.GetShortLinkStats(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestDeleteShortLinkNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		deleteResp: &proto.DeleteShortUrlResponse{Deleted: false},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/short-links/missing", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "code", Value: "missing"}}
	c.Request = req

	handler.DeleteShortLink(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestRedirectReturnsGoneForExpiredShortLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		getOriginResp: &proto.GetOriginUrlResponse{Status: statusExpired},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodGet, "/000001", nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.Redirect(c)

	if w.Code != http.StatusGone {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusGone, w.Body.String())
	}
}

func TestRedirectSendsVisitMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		getOriginResp: &proto.GetOriginUrlResponse{
			OriginUrl: "https://example.com",
			Status:    statusActive,
		},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	req := httptest.NewRequest(http.MethodGet, "/000001", nil)
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("Referer", "https://referer.example")
	req.RemoteAddr = "192.0.2.1:1234"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.Redirect(c)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusMovedPermanently, w.Body.String())
	}
	if client.originRequest == nil {
		t.Fatal("originRequest = nil")
	}
	if client.originRequest.UserAgent != "test-agent" || client.originRequest.Referer != "https://referer.example" {
		t.Fatalf("originRequest = %#v, want visit metadata", client.originRequest)
	}
}

func TestFrontendRoutesPreserveShortCodeRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		getOriginResp: &proto.GetOriginUrlResponse{
			OriginUrl: "https://example.com/landing",
			Status:    statusActive,
		},
	}
	handler := &Handler{
		rpcClient: client,
		baseURL:   "http://localhost:8080",
	}
	router := NewRouter(handler, RouterOptions{})
	req := httptest.NewRequest(http.MethodGet, "/000001", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusMovedPermanently, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "https://example.com/landing" {
		t.Fatalf("Location = %q, want %q", got, "https://example.com/landing")
	}
	if client.originRequest == nil || client.originRequest.ShortCode != "000001" {
		t.Fatalf("originRequest = %#v, want short code 000001", client.originRequest)
	}
}

func TestCreateShortLinkMapsRPCInvalidArgument(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &fakeShortUrlClient{
		createErr: status.Error(codes.InvalidArgument, "invalid short link request"),
	}
	handler := &Handler{rpcClient: client, baseURL: "http://localhost:8080"}
	req := httptest.NewRequest(http.MethodPost, "/api/short-links", bytes.NewBufferString(`{"origin_url":"https://example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	handler.CreateShortLink(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}
