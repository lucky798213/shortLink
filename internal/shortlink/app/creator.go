package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"short_url/internal/shortlink"
	"short_url/pkg/generator"
)

var ErrIDAllocatorUnavailable = errors.New("short link id allocator is unavailable")

type Creator struct {
	store     BatchLinkStore
	allocator IDAllocator
	buffer    *CreateBuffer
	bloom     BloomFilter
	now       func() time.Time
}

func newCreator(store BatchLinkStore, allocator IDAllocator, bloom BloomFilter, opts CreateOptions) *Creator {
	creator := &Creator{
		store:     store,
		allocator: allocator,
		bloom:     bloom,
		now:       time.Now,
	}
	if opts.Buffered && store != nil && allocator != nil {
		creator.buffer = NewCreateBuffer(store, allocator, CreateBufferOptions{
			QueueSize:      opts.QueueSize,
			BatchSize:      opts.BatchSize,
			FlushInterval:  opts.FlushInterval,
			EnqueueTimeout: opts.EnqueueTimeout,
			Now:            creator.nowTime,
		})
	}
	return creator
}

func (c *Creator) Create(ctx context.Context, originURL string, expireAtUnix int64) (string, error) {
	originURL, err := shortlink.NormalizeOriginURL(originURL)
	if err != nil {
		return "", err
	}
	expireAt, err := shortlink.ParseExpireAt(expireAtUnix, c.nowTime())
	if err != nil {
		return "", err
	}

	if c.buffer != nil {
		shortCode, err := c.buffer.Create(ctx, originURL, expireAt)
		if err != nil {
			return "", err
		}
		c.addBloom(ctx, shortCode)
		slog.Info("short url created", "short_code", shortCode, "expire_at", expireAtUnix, "write_mode", "batch")
		return shortCode, nil
	}
	if c.store == nil || c.allocator == nil {
		return "", ErrIDAllocatorUnavailable
	}

	id, err := c.allocator.NextID(ctx)
	if err != nil {
		return "", fmt.Errorf("allocate short url id: %w", err)
	}
	if id == 0 || id > generator.MaxID {
		return "", fmt.Errorf("allocate short url id: invalid id %d", id)
	}
	shortCode := generator.Encode(id)
	if err := c.store.BatchCreate(ctx, []shortlink.CreateInput{{
		ID:        id,
		ShortCode: shortCode,
		OriginURL: originURL,
		CreatedAt: c.nowTime(),
		ExpireAt:  expireAt,
	}}); err != nil {
		return "", fmt.Errorf("create short url: %w", err)
	}
	c.addBloom(ctx, shortCode)
	slog.Info("short url created", "id", id, "short_code", shortCode, "expire_at", expireAtUnix, "write_mode", "direct")
	return shortCode, nil
}

func (c *Creator) Shutdown(ctx context.Context) error {
	if c.buffer == nil {
		return nil
	}
	return c.buffer.Shutdown(ctx)
}

func (c *Creator) addBloom(ctx context.Context, shortCode string) {
	if c.bloom == nil {
		return
	}
	if err := c.bloom.Add(ctx, shortCode); err != nil {
		slog.Warn("short url bloom add failed", "short_code", shortCode, "err", err)
	}
}

func (c *Creator) nowTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}
