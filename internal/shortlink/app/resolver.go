package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"golang.org/x/sync/singleflight"

	"short_url/internal/shortlink"
	"short_url/internal/shortlink/code"
)

type Resolver struct {
	store         LinkStore
	remoteCache   Cache
	localCache    Cache
	bloom         BloomFilter
	defaultTTL    time.Duration
	notFoundTTL   time.Duration
	localTTL      time.Duration
	jitterRatio   float64
	lookupTimeout time.Duration
	lookupGroup   singleflight.Group
	now           func() time.Time
	jitter        func(time.Duration, float64) time.Duration
}

func newResolver(store LinkStore, remoteCache Cache, localCache Cache, bloom BloomFilter, opts CacheOptions) *Resolver {
	if remoteCache == nil {
		remoteCache = noopCache{}
	}
	if localCache == nil {
		localCache = noopCache{}
	}
	if opts.DefaultTTL <= 0 {
		opts.DefaultTTL = 24 * time.Hour
	}
	if opts.NotFoundTTL <= 0 {
		opts.NotFoundTTL = time.Minute
	}
	if opts.LocalTTL <= 0 {
		opts.LocalTTL = 5 * time.Minute
	}
	if opts.LookupTimeout <= 0 {
		opts.LookupTimeout = 3 * time.Second
	}
	return &Resolver{
		store:         store,
		remoteCache:   remoteCache,
		localCache:    localCache,
		bloom:         bloom,
		defaultTTL:    opts.DefaultTTL,
		notFoundTTL:   opts.NotFoundTTL,
		localTTL:      opts.LocalTTL,
		jitterRatio:   opts.JitterRatio,
		lookupTimeout: opts.LookupTimeout,
		now:           time.Now,
		jitter:        randomJitter,
	}
}

func (r *Resolver) Resolve(ctx context.Context, shortCode string) (*LinkResult, error) {
	if !code.IsValid(shortCode) {
		return &LinkResult{Status: shortlink.StatusNotFound}, nil
	}
	if cached, ok := r.getCache(ctx, r.localCache, shortCode, false); ok {
		return cached, nil
	}
	if cached, ok := r.getCache(ctx, r.remoteCache, shortCode, true); ok {
		return cached, nil
	}
	if r.bloom != nil {
		exists, err := r.bloom.Exists(ctx, shortCode)
		if err != nil {
			slog.Warn("short url bloom check failed, fallback to mysql", "short_code", shortCode, "err", err)
		} else if !exists {
			result := &LinkResult{Status: shortlink.StatusNotFound}
			entry := CacheEntry{ShortCode: shortCode, Status: shortlink.StatusNotFound}
			r.setRemoteCache(ctx, entry, r.jitterTTL(r.notFoundTTL))
			r.setLocalCache(ctx, entry, r.localTTLFor(nil, shortlink.StatusNotFound))
			return result, nil
		}
	}

	resultCh := r.lookupGroup.DoChan(shortCode, func() (any, error) {
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.lookupTimeout)
		defer cancel()
		if cached, ok := r.getCache(lookupCtx, r.remoteCache, shortCode, true); ok {
			return cached, nil
		}
		return r.loadFromStore(lookupCtx, shortCode)
	})
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		linkResult, ok := result.Val.(*LinkResult)
		if !ok {
			return nil, fmt.Errorf("unexpected singleflight result for short_code %q", shortCode)
		}
		return linkResult, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Resolver) DeleteCaches(ctx context.Context, shortCode string) {
	if err := r.localCache.Delete(ctx, shortCode); err != nil {
		slog.Warn("short url local cache delete failed", "short_code", shortCode, "err", err)
	}
	if err := r.remoteCache.Delete(ctx, shortCode); err != nil {
		slog.Warn("short url cache delete failed", "short_code", shortCode, "err", err)
	}
}

func (r *Resolver) loadFromStore(ctx context.Context, shortCode string) (*LinkResult, error) {
	if r.store == nil {
		return nil, errors.New("short link store is unavailable")
	}
	link, err := r.store.FindByShortCode(ctx, shortCode)
	if err != nil {
		return nil, fmt.Errorf("get origin url: %w", err)
	}
	if link == nil {
		result := &LinkResult{Status: shortlink.StatusNotFound}
		entry := CacheEntry{ShortCode: shortCode, Status: shortlink.StatusNotFound}
		r.setRemoteCache(ctx, entry, r.jitterTTL(r.notFoundTTL))
		r.setLocalCache(ctx, entry, r.localTTLFor(nil, shortlink.StatusNotFound))
		return result, nil
	}
	status := link.StatusAt(r.nowTime())
	result := &LinkResult{Link: link, Status: status}
	entry := NewCacheEntry(link, status)
	r.setRemoteCache(ctx, entry, r.cacheTTL(link, status))
	r.setLocalCache(ctx, entry, r.localTTLFor(link, status))
	return result, nil
}

func (r *Resolver) getCache(ctx context.Context, cache Cache, shortCode string, warmLocal bool) (*LinkResult, bool) {
	entry, err := cache.Get(ctx, shortCode)
	if errors.Is(err, ErrCacheMiss) {
		return nil, false
	}
	if err != nil {
		slog.Warn("short url cache get failed, fallback to mysql", "short_code", shortCode, "err", err)
		return nil, false
	}
	if entry == nil {
		return nil, false
	}
	if entry.Status == shortlink.StatusNotFound {
		if warmLocal {
			r.setLocalCache(ctx, *entry, r.localTTLFor(nil, shortlink.StatusNotFound))
		}
		return &LinkResult{Status: shortlink.StatusNotFound}, true
	}
	link := entry.ToLink()
	if link == nil {
		return &LinkResult{Status: shortlink.StatusNotFound}, true
	}
	status := entry.Status
	if status == "" {
		status = link.StatusAt(r.nowTime())
	}
	if link.ExpireAt != nil && !link.ExpireAt.After(r.nowTime()) {
		status = shortlink.StatusExpired
	}
	if warmLocal {
		entry.Status = status
		r.setLocalCache(ctx, *entry, r.localTTLFor(link, status))
	}
	return &LinkResult{Link: link, Status: status}, true
}

func (r *Resolver) setRemoteCache(ctx context.Context, entry CacheEntry, ttl time.Duration) {
	if err := r.remoteCache.Set(ctx, entry, ttl); err != nil {
		slog.Warn("short url cache set failed", "short_code", entry.ShortCode, "status", entry.Status, "err", err)
	}
}

func (r *Resolver) setLocalCache(ctx context.Context, entry CacheEntry, ttl time.Duration) {
	if err := r.localCache.Set(ctx, entry, ttl); err != nil {
		slog.Warn("short url local cache set failed", "short_code", entry.ShortCode, "status", entry.Status, "err", err)
	}
}

func (r *Resolver) cacheTTL(link *shortlink.Link, status shortlink.Status) time.Duration {
	if status == shortlink.StatusActive && link.ExpireAt != nil {
		if ttl := link.ExpireAt.Sub(r.nowTime()); ttl > 0 {
			return ttl
		}
	}
	return r.jitterTTL(r.defaultTTL)
}

func (r *Resolver) localTTLFor(link *shortlink.Link, status shortlink.Status) time.Duration {
	if status == shortlink.StatusActive && link != nil && link.ExpireAt != nil {
		if ttl := link.ExpireAt.Sub(r.nowTime()); ttl > 0 && ttl < r.localTTL {
			return ttl
		}
	}
	return r.localTTL
}

func (r *Resolver) jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 || r.jitterRatio <= 0 {
		return ttl
	}
	return r.jitter(ttl, r.jitterRatio)
}

func (r *Resolver) nowTime() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func randomJitter(ttl time.Duration, ratio float64) time.Duration {
	if ttl <= 0 || ratio <= 0 {
		return ttl
	}
	factor := 1 + ((rand.Float64()*2 - 1) * ratio)
	if factor <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * factor)
}

type noopCache struct{}

func (noopCache) Get(context.Context, string) (*CacheEntry, error)     { return nil, ErrCacheMiss }
func (noopCache) Set(context.Context, CacheEntry, time.Duration) error { return nil }
func (noopCache) Delete(context.Context, string) error                 { return nil }
