package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"short_url/internal/shortlink/app"
)

var ErrMiss = app.ErrCacheMiss

func Key(shortCode string) string {
	return "short_url:code:" + shortCode
}

type RedisShortUrlCache struct {
	client redis.Cmdable
}

func NewRedisShortUrlCache(client redis.Cmdable) *RedisShortUrlCache {
	return &RedisShortUrlCache{client: client}
}

func (c *RedisShortUrlCache) Get(ctx context.Context, shortCode string) (*app.CacheEntry, error) {
	payload, err := c.client.Get(ctx, Key(shortCode)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, fmt.Errorf("redis get short url: %w", err)
	}

	var entry app.CacheEntry
	if err := json.Unmarshal([]byte(payload), &entry); err != nil {
		return nil, fmt.Errorf("unmarshal short url cache: %w", err)
	}
	return &entry, nil
}

func (c *RedisShortUrlCache) Set(ctx context.Context, entry app.CacheEntry, ttl time.Duration) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal short url cache: %w", err)
	}
	if err := c.client.Set(ctx, Key(entry.ShortCode), payload, ttl).Err(); err != nil {
		return fmt.Errorf("redis set short url: %w", err)
	}
	return nil
}

func (c *RedisShortUrlCache) Delete(ctx context.Context, shortCode string) error {
	if err := c.client.Del(ctx, Key(shortCode)).Err(); err != nil {
		return fmt.Errorf("redis delete short url: %w", err)
	}
	return nil
}

type NoopShortUrlCache struct{}

func NewNoopShortUrlCache() NoopShortUrlCache {
	return NoopShortUrlCache{}
}

func (NoopShortUrlCache) Get(context.Context, string) (*app.CacheEntry, error) {
	return nil, ErrMiss
}

func (NoopShortUrlCache) Set(context.Context, app.CacheEntry, time.Duration) error {
	return nil
}

func (NoopShortUrlCache) Delete(context.Context, string) error {
	return nil
}

type localEntry struct {
	value     app.CacheEntry
	expiresAt time.Time
}

type LocalShortUrlCache struct {
	mu         sync.RWMutex
	items      map[string]localEntry
	maxEntries int
	now        func() time.Time
}

func NewLocalShortUrlCache(maxEntries int) *LocalShortUrlCache {
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	return &LocalShortUrlCache{
		items:      make(map[string]localEntry),
		maxEntries: maxEntries,
		now:        time.Now,
	}
}

func (c *LocalShortUrlCache) Get(ctx context.Context, shortCode string) (*app.CacheEntry, error) {
	_ = ctx
	key := Key(shortCode)
	now := c.nowTime()

	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return nil, ErrMiss
	}
	if !item.expiresAt.After(now) {
		c.mu.Lock()
		delete(c.items, key)
		c.mu.Unlock()
		return nil, ErrMiss
	}

	entry := item.value
	return &entry, nil
}

func (c *LocalShortUrlCache) Set(ctx context.Context, entry app.CacheEntry, ttl time.Duration) error {
	_ = ctx
	if ttl <= 0 {
		return nil
	}

	key := Key(entry.ShortCode)
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxEntries {
		for oldKey := range c.items {
			delete(c.items, oldKey)
			break
		}
	}
	c.items[key] = localEntry{
		value:     entry,
		expiresAt: c.nowTime().Add(ttl),
	}
	return nil
}

func (c *LocalShortUrlCache) Delete(ctx context.Context, shortCode string) error {
	_ = ctx
	c.mu.Lock()
	delete(c.items, Key(shortCode))
	c.mu.Unlock()
	return nil
}

func (c *LocalShortUrlCache) nowTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
