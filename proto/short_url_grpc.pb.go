package proto

import (
	"context"

	"google.golang.org/grpc"
)

// 本文件是 gRPC 的客户端/服务端 stub 代码，手写生成。
// 包含三个核心部分：
// 1. ShortUrlClient 接口 + 实现 —— 供 Web 层调用 RPC 服务
// 2. ShortUrlServer 接口 + UnimplementedShortUrlServer —— 供 RPC 层实现
// 3. RegisterShortUrlServer —— 将服务端实现注册到 gRPC Server

// ========================================
// 客户端部分
// ========================================

// ShortUrlClient 是 ShortUrl 服务的客户端接口。
// Web 层通过此接口调用 RPC 服务，无需关心底层网络细节。
type ShortUrlClient interface {
	// CreateShortUrl 调用远程的 CreateShortUrl RPC，传入长链接，获取短码。
	CreateShortUrl(ctx context.Context, in *CreateShortUrlRequest, opts ...grpc.CallOption) (*CreateShortUrlResponse, error)
	// GetOriginUrl 调用远程的 GetOriginUrl RPC，传入短码，获取原始长链接。
	GetOriginUrl(ctx context.Context, in *GetOriginUrlRequest, opts ...grpc.CallOption) (*GetOriginUrlResponse, error)
	// GetShortUrl 调用远程的 GetShortUrl RPC，获取短链接详情。
	GetShortUrl(ctx context.Context, in *GetShortUrlRequest, opts ...grpc.CallOption) (*GetShortUrlResponse, error)
	// DeleteShortUrl 调用远程的 DeleteShortUrl RPC，软删除短链接。
	DeleteShortUrl(ctx context.Context, in *DeleteShortUrlRequest, opts ...grpc.CallOption) (*DeleteShortUrlResponse, error)
	// GetShortUrlStats 调用远程的 GetShortUrlStats RPC，获取访问统计。
	GetShortUrlStats(ctx context.Context, in *GetShortUrlStatsRequest, opts ...grpc.CallOption) (*GetShortUrlStatsResponse, error)
}

// shortUrlClient 是 ShortUrlClient 的具体实现。
// cc 是 gRPC 连接接口，可以是真实连接或连接池。
type shortUrlClient struct {
	cc grpc.ClientConnInterface
}

// NewShortUrlClient 创建一个 gRPC 客户端。
// cc 通常是通过 grpc.NewClient(addr, opts...) 建立的连接。
func NewShortUrlClient(cc grpc.ClientConnInterface) ShortUrlClient {
	return &shortUrlClient{cc}
}

func shortURLCallOptions(opts []grpc.CallOption) []grpc.CallOption {
	callOpts := make([]grpc.CallOption, 0, len(opts)+1)
	callOpts = append(callOpts, grpc.ForceCodec(shortURLJSONCodec{}))
	callOpts = append(callOpts, opts...)
	return callOpts
}

// CreateShortUrl 发起 CreateShortUrl RPC 调用。
// 路径 "/short_url.ShortUrl/CreateShortUrl" 由 protobuf 规范定义：
// 格式为 /{package}.{Service}/{Method}。
func (c *shortUrlClient) CreateShortUrl(ctx context.Context, in *CreateShortUrlRequest, opts ...grpc.CallOption) (*CreateShortUrlResponse, error) {
	out := new(CreateShortUrlResponse)

	//发起远程调用，创建短链接。
	err := c.cc.Invoke(ctx, "/short_url.ShortUrl/CreateShortUrl", in, out, shortURLCallOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetOriginUrl 发起 GetOriginUrl RPC 调用。
func (c *shortUrlClient) GetOriginUrl(ctx context.Context, in *GetOriginUrlRequest, opts ...grpc.CallOption) (*GetOriginUrlResponse, error) {
	out := new(GetOriginUrlResponse)
	err := c.cc.Invoke(ctx, "/short_url.ShortUrl/GetOriginUrl", in, out, shortURLCallOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetShortUrl 发起 GetShortUrl RPC 调用。
func (c *shortUrlClient) GetShortUrl(ctx context.Context, in *GetShortUrlRequest, opts ...grpc.CallOption) (*GetShortUrlResponse, error) {
	out := new(GetShortUrlResponse)
	err := c.cc.Invoke(ctx, "/short_url.ShortUrl/GetShortUrl", in, out, shortURLCallOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteShortUrl 发起 DeleteShortUrl RPC 调用。
func (c *shortUrlClient) DeleteShortUrl(ctx context.Context, in *DeleteShortUrlRequest, opts ...grpc.CallOption) (*DeleteShortUrlResponse, error) {
	out := new(DeleteShortUrlResponse)
	err := c.cc.Invoke(ctx, "/short_url.ShortUrl/DeleteShortUrl", in, out, shortURLCallOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GetShortUrlStats 发起 GetShortUrlStats RPC 调用。
func (c *shortUrlClient) GetShortUrlStats(ctx context.Context, in *GetShortUrlStatsRequest, opts ...grpc.CallOption) (*GetShortUrlStatsResponse, error) {
	out := new(GetShortUrlStatsResponse)
	err := c.cc.Invoke(ctx, "/short_url.ShortUrl/GetShortUrlStats", in, out, shortURLCallOptions(opts)...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ========================================
// 服务端部分
// ========================================

// ShortUrlServer 是 ShortUrl 服务的服务端接口。
// RPC 层的 grpc 包需要实现此接口。
type ShortUrlServer interface {
	CreateShortUrl(context.Context, *CreateShortUrlRequest) (*CreateShortUrlResponse, error)
	GetOriginUrl(context.Context, *GetOriginUrlRequest) (*GetOriginUrlResponse, error)
	GetShortUrl(context.Context, *GetShortUrlRequest) (*GetShortUrlResponse, error)
	DeleteShortUrl(context.Context, *DeleteShortUrlRequest) (*DeleteShortUrlResponse, error)
	GetShortUrlStats(context.Context, *GetShortUrlStatsRequest) (*GetShortUrlStatsResponse, error)
}

// UnimplementedShortUrlServer 提供所有 RPC 方法的默认空实现。
// 服务端实现通过嵌入此结构体来获得"前向兼容"能力：
// 将来新增 RPC 方法时，已有代码不需要修改就能编译通过。
type UnimplementedShortUrlServer struct{}

func (UnimplementedShortUrlServer) CreateShortUrl(context.Context, *CreateShortUrlRequest) (*CreateShortUrlResponse, error) {
	return nil, nil
}
func (UnimplementedShortUrlServer) GetOriginUrl(context.Context, *GetOriginUrlRequest) (*GetOriginUrlResponse, error) {
	return nil, nil
}
func (UnimplementedShortUrlServer) GetShortUrl(context.Context, *GetShortUrlRequest) (*GetShortUrlResponse, error) {
	return nil, nil
}
func (UnimplementedShortUrlServer) DeleteShortUrl(context.Context, *DeleteShortUrlRequest) (*DeleteShortUrlResponse, error) {
	return nil, nil
}
func (UnimplementedShortUrlServer) GetShortUrlStats(context.Context, *GetShortUrlStatsRequest) (*GetShortUrlStatsResponse, error) {
	return nil, nil
}

// RegisterShortUrlServer 将服务端实现注册到 gRPC Server。
//
// 工作原理：
// 1. 构建一个 ServiceDesc，描述服务的名称、方法列表
// 2. 每个方法包含一个 MethodHandler，负责：解码请求 → 调用业务逻辑 → 编码响应
// 3. 调用 s.RegisterService 完成注册

// 把 shortUrlService 这个业务实现，挂到 grpcServer 上
// 我这里有一个服务，服务名叫 short_url.ShortUrl，它下面有几个 RPC 方法。
// 当客户端调用这些方法时，你应该调用 srv 上对应的 Go 方法。
func RegisterShortUrlServer(s *grpc.Server, srv ShortUrlServer) {
	s.RegisterService(&grpc.ServiceDesc{

		//gRPC 服务名。
		//它通常来自 .proto 文件里的定义
		ServiceName: "short_url.ShortUrl",

		//它表示：
		//这个服务对应的 Go 接口类型是 ShortUrlServer
		HandlerType: (*ShortUrlServer)(nil),

		//这里定义了这个 gRPC 服务有哪些普通方法。
		//你的服务有 5 个一元 RPC 方法：
		Methods: []grpc.MethodDesc{
			{
				MethodName: "CreateShortUrl",
				// MethodHandler 包装了解码和调用的完整流程：
				// dec 函数将 protobuf 二进制数据解码为请求对象，
				// 然后调用 srv.CreateShortUrl 执行实际业务逻辑。
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					//创建请求对象
					in := new(CreateShortUrlRequest)

					//dec 是 gRPC 框架传进来的解码函数。
					//它的作用是：
					//把客户端发来的 protobuf 二进制请求，反序列化到 in 这个 Go 结构体里。
					if err := dec(in); err != nil {
						return nil, err
					}

					//把 srv 转成 ShortUrlServer，gRPC 这里要把它断言成真正的服务接口
					return srv.(ShortUrlServer).CreateShortUrl(ctx, in)
				},
			},
			{
				MethodName: "GetOriginUrl",
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(GetOriginUrlRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.(ShortUrlServer).GetOriginUrl(ctx, in)
				},
			},
			{
				MethodName: "GetShortUrl",
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(GetShortUrlRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.(ShortUrlServer).GetShortUrl(ctx, in)
				},
			},
			{
				MethodName: "DeleteShortUrl",
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(DeleteShortUrlRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.(ShortUrlServer).DeleteShortUrl(ctx, in)
				},
			},
			{
				MethodName: "GetShortUrlStats",
				Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(GetShortUrlStatsRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.(ShortUrlServer).GetShortUrlStats(ctx, in)
				},
			},
		},

		//这里表示这个服务没有流式 RPC
		Streams: []grpc.StreamDesc{},

		//表示这个服务来自哪个 proto 文件
		Metadata: "short_url.proto",
	}, srv)
}
