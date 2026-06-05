package service

import (
	"context"
	"testing"
	"time"

	"short_url/rpc/repository"
	cachepkg "short_url/rpc/repository/cache"
)

func BenchmarkGetOriginUrlLocalCacheHit(b *testing.B) {
	local := cachepkg.NewLocalShortUrlCache(1000)
	entry := cachepkg.NewShortUrlEntry(&repository.ShortUrl{
		ShortCode: "000001",
		OriginURL: "https://example.com",
		CreatedAt: time.Unix(100, 0),
	}, StatusActive)
	if err := local.Set(context.Background(), entry, time.Hour); err != nil {
		b.Fatalf("set local cache: %v", err)
	}
	svc := NewShortUrlServiceWithOptions(&mockShortUrlRepo{}, ShortUrlServiceOptions{
		RemoteCache: cachepkg.NewNoopShortUrlCache(),
		LocalCache:  local,
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.GetOriginUrl(context.Background(), "000001", nil)
		if err != nil {
			b.Fatalf("GetOriginUrl() error: %v", err)
		}
		if got.Status != StatusActive || got.Row == nil {
			b.Fatalf("unexpected result: %#v", got)
		}
	}
}
