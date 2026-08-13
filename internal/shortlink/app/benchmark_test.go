package app

import (
	"context"
	"testing"
	"time"

	"short_url/internal/shortlink"
)

func BenchmarkGetOriginUrlLocalCacheHit(b *testing.B) {
	entry := NewCacheEntry(&shortlink.Link{
		ShortCode: "000001",
		OriginURL: "https://example.com",
		CreatedAt: time.Unix(100, 0),
	}, StatusActive)
	local := &mockShortUrlCache{getEntry: &entry}
	svc := NewShortUrlServiceWithOptions(&mockShortUrlRepo{}, ShortUrlServiceOptions{
		RemoteCache: noopCache{},
		LocalCache:  local,
	})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := svc.GetOriginUrl(context.Background(), "000001", nil)
		if err != nil {
			b.Fatalf("GetOriginUrl() error: %v", err)
		}
		if got.Status != StatusActive || got.Link == nil {
			b.Fatalf("unexpected result: %#v", got)
		}
	}
}
