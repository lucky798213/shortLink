package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"short_url/internal/shortlink"
)

var ErrMiss = errors.New("short url cache miss")

type ShortUrlCache interface {
	Get(ctx context.Context, shortCode string) (*ShortUrlEntry, error)
	Set(ctx context.Context, entry ShortUrlEntry, ttl time.Duration) error
	Delete(ctx context.Context, shortCode string) error
}

type ShortUrlEntry struct {
	ShortCode string           `json:"short_code"`
	OriginURL string           `json:"origin_url"`
	CreatedAt int64            `json:"created_at"`
	ExpireAt  int64            `json:"expire_at"`
	Status    shortlink.Status `json:"status"`
}

func NewShortUrlEntry(row *shortlink.Link, status shortlink.Status) ShortUrlEntry {
	entry := ShortUrlEntry{
		ShortCode: row.ShortCode,
		OriginURL: row.OriginURL,
		CreatedAt: row.CreatedAt.Unix(),
		Status:    status,
	}
	if row.ExpireAt != nil {
		entry.ExpireAt = row.ExpireAt.Unix()
	}
	return entry
}

func (e ShortUrlEntry) ToRow() *shortlink.Link {
	if e.ShortCode == "" && e.OriginURL == "" {
		return nil
	}

	row := &shortlink.Link{
		ShortCode: e.ShortCode,
		OriginURL: e.OriginURL,
		CreatedAt: time.Unix(e.CreatedAt, 0),
	}
	if e.ExpireAt != 0 {
		expireAt := time.Unix(e.ExpireAt, 0)
		row.ExpireAt = &expireAt
	}
	return row
}

func Key(shortCode string) string {
	return "short_url:code:" + shortCode
}

type RedisShortUrlCache struct {
	client redis.Cmdable
}

func NewRedisShortUrlCache(client redis.Cmdable) *RedisShortUrlCache {
	return &RedisShortUrlCache{client: client}
}

func (c *RedisShortUrlCache) Get(ctx context.Context, shortCode string) (*ShortUrlEntry, error) {
	payload, err := c.client.Get(ctx, Key(shortCode)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	if err != nil {
		return nil, fmt.Errorf("redis get short url: %w", err)
	}

	var entry ShortUrlEntry
	if err := json.Unmarshal([]byte(payload), &entry); err != nil {
		return nil, fmt.Errorf("unmarshal short url cache: %w", err)
	}
	return &entry, nil
}

func (c *RedisShortUrlCache) Set(ctx context.Context, entry ShortUrlEntry, ttl time.Duration) error {
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

func (NoopShortUrlCache) Get(context.Context, string) (*ShortUrlEntry, error) {
	return nil, ErrMiss
}

func (NoopShortUrlCache) Set(context.Context, ShortUrlEntry, time.Duration) error {
	return nil
}

func (NoopShortUrlCache) Delete(context.Context, string) error {
	return nil
}

type localEntry struct {
	value     ShortUrlEntry
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

func (c *LocalShortUrlCache) Get(ctx context.Context, shortCode string) (*ShortUrlEntry, error) {
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

func (c *LocalShortUrlCache) Set(ctx context.Context, entry ShortUrlEntry, ttl time.Duration) error {
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
