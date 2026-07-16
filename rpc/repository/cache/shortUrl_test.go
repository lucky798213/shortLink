package cache

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"short_url/internal/shortlink/app"
)

func TestCacheEntryToLink(t *testing.T) {
	entry := app.CacheEntry{
		ShortCode: "000001",
		OriginURL: "https://example.com",
		CreatedAt: 100,
		ExpireAt:  200,
		Status:    "active",
	}

	link := entry.ToLink()
	if link == nil {
		t.Fatal("ToLink() = nil, want link")
	}
	if link.ShortCode != "000001" || link.OriginURL != "https://example.com" {
		t.Fatalf("link = %#v, want short_code and origin_url", link)
	}
	if link.CreatedAt.Unix() != 100 {
		t.Fatalf("CreatedAt = %d, want 100", link.CreatedAt.Unix())
	}
	if link.ExpireAt == nil || link.ExpireAt.Unix() != 200 {
		t.Fatalf("ExpireAt = %v, want 200", link.ExpireAt)
	}
}

func TestLocalShortUrlCache(t *testing.T) {
	local := NewLocalShortUrlCache(2)
	now := time.Unix(100, 0)
	local.now = func() time.Time { return now }

	entry := app.CacheEntry{
		ShortCode: "000001",
		OriginURL: "https://example.com",
		Status:    "active",
	}
	if err := local.Set(context.Background(), entry, time.Minute); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	got, err := local.Get(context.Background(), "000001")
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.ShortCode != "000001" {
		t.Fatalf("ShortCode = %q, want 000001", got.ShortCode)
	}

	now = now.Add(2 * time.Minute)
	_, err = local.Get(context.Background(), "000001")
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() after expiry error = %v, want ErrMiss", err)
	}
}

func TestLocalShortUrlCacheDelete(t *testing.T) {
	local := NewLocalShortUrlCache(2)
	entry := app.CacheEntry{ShortCode: "000001", Status: "not_found"}
	if err := local.Set(context.Background(), entry, time.Minute); err != nil {
		t.Fatalf("Set() error: %v", err)
	}
	if err := local.Delete(context.Background(), "000001"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	_, err := local.Get(context.Background(), "000001")
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() after delete error = %v, want ErrMiss", err)
	}
}

func TestRedisShortUrlCacheIntegration(t *testing.T) {
	addr := os.Getenv("SHORT_URL_REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("set SHORT_URL_REDIS_TEST_ADDR to run Redis integration test")
	}

	ctx := context.Background()
	client := redis.NewClient(&redis.Options{Addr: addr})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	cache := NewRedisShortUrlCache(client)
	entry := app.CacheEntry{
		ShortCode: "redis-test",
		OriginURL: "https://example.com/redis",
		CreatedAt: time.Now().Unix(),
		Status:    "active",
	}
	defer cache.Delete(ctx, entry.ShortCode)

	if err := cache.Set(ctx, entry, time.Minute); err != nil {
		t.Fatalf("Set() error: %v", err)
	}

	got, err := cache.Get(ctx, entry.ShortCode)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if got.ShortCode != entry.ShortCode || got.OriginURL != entry.OriginURL || got.Status != entry.Status {
		t.Fatalf("Get() = %#v, want %#v", got, entry)
	}

	if err := cache.Delete(ctx, entry.ShortCode); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	_, err = cache.Get(ctx, entry.ShortCode)
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("Get() after delete error = %v, want ErrMiss", err)
	}
}
