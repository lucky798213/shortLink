package grpcserver

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	proto "short_url/api/shortlink/v1"
	shortlinkapp "short_url/internal/shortlink/app"

	gogrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestShortUrlServerMapsValidationError(t *testing.T) {
	server := NewShortUrlServer(shortlinkapp.NewService(nil, shortlinkapp.Options{}))

	_, err := server.CreateShortUrl(context.Background(), &proto.CreateShortUrlRequest{
		OriginUrl: "ftp://example.com",
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateShortUrl() code = %s, want %s; error=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func TestGeneratedGRPCServerInvokesUnaryInterceptor(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	var intercepted atomic.Bool
	grpcServer := gogrpc.NewServer(gogrpc.UnaryInterceptor(
		func(ctx context.Context, req any, info *gogrpc.UnaryServerInfo, handler gogrpc.UnaryHandler) (any, error) {
			intercepted.Store(true)
			return handler(ctx, req)
		},
	))
	proto.RegisterShortLinkServiceServer(grpcServer, NewShortUrlServer(shortlinkapp.NewService(nil, shortlinkapp.Options{})))
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	conn, err := gogrpc.NewClient(
		"passthrough:///bufnet",
		gogrpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		gogrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := proto.NewShortLinkServiceClient(conn)
	_, err = client.CreateShortUrl(context.Background(), &proto.CreateShortUrlRequest{OriginUrl: "ftp://example.com"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateShortUrl() code = %s, want %s; error=%v", status.Code(err), codes.InvalidArgument, err)
	}
	if !intercepted.Load() {
		t.Fatal("generated gRPC handler did not invoke unary interceptor")
	}
}
