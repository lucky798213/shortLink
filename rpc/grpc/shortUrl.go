package grpc

import (
	"context"

	proto "short_url/api/shortlink/v1"
	"short_url/internal/shortlink"
	"short_url/rpc/service"
)

// ShortUrlServer 是 gRPC ShortUrl 服务的具体实现。
//
// 它嵌入 UnimplementedShortUrlServer 的原因（gRPC 的最佳实践）：
// 将来 .proto 文件新增 RPC 方法时，UnimplementedShortUrlServer 会提供默认空实现，
// 这样现有代码无需修改就能编译通过，实现了"前向兼容"。
type ShortUrlServer struct {
	proto.UnimplementedShortLinkServiceServer
	svc *service.ShortUrlService
}

// NewShortUrlServer 创建 gRPC 服务端实例。
func NewShortUrlServer(svc *service.ShortUrlService) *ShortUrlServer {
	return &ShortUrlServer{svc: svc}
}

// CreateShortUrl 处理创建短链接的 gRPC 请求。
//
// 本方法只做协议适配：
// 1. 从 protobuf 请求中提取 origin_url
// 2. 调用 Service 层执行业务逻辑
// 3. 将结果封装为 protobuf 响应
//
// 为什么不在 Handler 层直接操作数据库：
// Handler 的职责是协议转换，不应该包含业务逻辑。
// 这样当需要支持 HTTP/REST 等其他协议时，可以复用 Service 层。
func (s *ShortUrlServer) CreateShortUrl(ctx context.Context, req *proto.CreateShortUrlRequest) (*proto.CreateShortUrlResponse, error) {
	shortCode, err := s.svc.CreateShortUrl(ctx, req.OriginUrl, req.ExpireAt)
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &proto.CreateShortUrlResponse{ShortCode: shortCode}, nil
}

// GetOriginUrl 处理查询原始链接的 gRPC 请求。
//
// 当 row 为 nil（短码不存在）时，返回空响应而非 error。
// 这样 Web 层可以根据 OriginUrl 是否为空来判断 404，而不是解析 error 字符串。
func (s *ShortUrlServer) GetOriginUrl(ctx context.Context, req *proto.GetOriginUrlRequest) (*proto.GetOriginUrlResponse, error) {
	result, err := s.svc.GetOriginUrl(ctx, req.ShortCode, &service.VisitInfo{
		ClientIP:  req.ClientIp,
		UserAgent: req.UserAgent,
		Referer:   req.Referer,
	})
	if err != nil {
		return nil, toGRPCError(err)
	}
	if result == nil || result.Row == nil {
		// 短码不存在，返回空响应（非 error），
		// 让调用方根据 OriginUrl=="" 判断为 404
		return &proto.GetOriginUrlResponse{Status: toProtoStatus(service.StatusNotFound)}, nil
	}

	resp := &proto.GetOriginUrlResponse{
		OriginUrl: result.Row.OriginURL,
		CreatedAt: result.Row.CreatedAt.Unix(), // time.Time → Unix 时间戳（秒）
		Status:    toProtoStatus(result.Status),
	}
	// ExpireAt 可能为 nil（未设置过期时间），需要判空
	if result.Row.ExpireAt != nil {
		resp.ExpireAt = result.Row.ExpireAt.Unix()
	}
	return resp, nil
}

// GetShortUrl 处理查询短链接详情的 gRPC 请求。
func (s *ShortUrlServer) GetShortUrl(ctx context.Context, req *proto.GetShortUrlRequest) (*proto.GetShortUrlResponse, error) {
	result, err := s.svc.GetShortUrl(ctx, req.ShortCode)
	if err != nil {
		return nil, toGRPCError(err)
	}
	if result == nil || result.Row == nil {
		return &proto.GetShortUrlResponse{Status: toProtoStatus(service.StatusNotFound)}, nil
	}

	resp := &proto.GetShortUrlResponse{
		ShortCode: result.Row.ShortCode,
		OriginUrl: result.Row.OriginURL,
		CreatedAt: result.Row.CreatedAt.Unix(),
		Status:    toProtoStatus(result.Status),
	}
	if result.Row.ExpireAt != nil {
		resp.ExpireAt = result.Row.ExpireAt.Unix()
	}
	return resp, nil
}

// DeleteShortUrl 处理软删除短链接的 gRPC 请求。
func (s *ShortUrlServer) DeleteShortUrl(ctx context.Context, req *proto.DeleteShortUrlRequest) (*proto.DeleteShortUrlResponse, error) {
	deleted, err := s.svc.DeleteShortUrl(ctx, req.ShortCode)
	if err != nil {
		return nil, toGRPCError(err)
	}
	return &proto.DeleteShortUrlResponse{Deleted: deleted}, nil
}

// GetShortUrlStats 处理查询短链接访问统计的 gRPC 请求。
func (s *ShortUrlServer) GetShortUrlStats(ctx context.Context, req *proto.GetShortUrlStatsRequest) (*proto.GetShortUrlStatsResponse, error) {
	result, err := s.svc.GetShortUrlStats(ctx, req.ShortCode)
	if err != nil {
		return nil, toGRPCError(err)
	}
	if result == nil || result.Status == service.StatusNotFound || result.Stats == nil {
		return &proto.GetShortUrlStatsResponse{Status: toProtoStatus(service.StatusNotFound)}, nil
	}

	resp := &proto.GetShortUrlStatsResponse{
		ShortCode:     result.Stats.ShortCode,
		Pv:            result.Stats.PV,
		Uv:            result.Stats.UV,
		Status:        toProtoStatus(result.Status),
		TopReferers:   toProtoStatsItems(result.Stats.TopReferers),
		TopUserAgents: toProtoStatsItems(result.Stats.TopUserAgents),
	}
	if result.Stats.LastVisitedAt != nil {
		resp.LastVisitedAt = result.Stats.LastVisitedAt.Unix()
	}
	return resp, nil
}

func toProtoStatus(value shortlink.Status) proto.ShortLinkStatus {
	switch value {
	case shortlink.StatusActive:
		return proto.ShortLinkStatus_SHORT_LINK_STATUS_ACTIVE
	case shortlink.StatusExpired:
		return proto.ShortLinkStatus_SHORT_LINK_STATUS_EXPIRED
	case shortlink.StatusNotFound:
		return proto.ShortLinkStatus_SHORT_LINK_STATUS_NOT_FOUND
	default:
		return proto.ShortLinkStatus_SHORT_LINK_STATUS_UNSPECIFIED
	}
}

func toProtoStatsItems(items []shortlink.StatsItem) []*proto.StatsItem {
	resp := make([]*proto.StatsItem, 0, len(items))
	for _, item := range items {
		resp = append(resp, &proto.StatsItem{
			Value: item.Value,
			Count: item.Count,
		})
	}
	return resp
}
