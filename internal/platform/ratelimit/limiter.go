package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const tokenBucketScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local burst = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local requested = tonumber(ARGV[5])

local data = redis.call("HMGET", key, "tokens", "ts")
local tokens = tonumber(data[1])
local ts = tonumber(data[2])
if tokens == nil then
  tokens = burst
  ts = now
end

local delta = math.max(0, now - ts) / 1000.0
tokens = math.min(burst, tokens + delta * rate)
local allowed = 0
if tokens >= requested then
  tokens = tokens - requested
  allowed = 1
end

redis.call("HMSET", key, "tokens", tokens, "ts", now)
redis.call("PEXPIRE", key, ttl)
return {allowed, tokens}
`

type TokenBucketLimiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type RedisTokenBucketLimiter struct {
	client redis.Cmdable
	prefix string
	rate   float64
	burst  int
	ttl    time.Duration
	script *redis.Script //Lua 脚本加载工具
}

func NewRedisTokenBucketLimiter(client redis.Cmdable, prefix string, rate float64, burst int) *RedisTokenBucketLimiter {
	if prefix == "" {
		prefix = "short_url:rate_limit:"
	}
	if rate <= 0 {
		rate = 100
	}
	if burst <= 0 {
		burst = int(rate)
	}
	return &RedisTokenBucketLimiter{
		client: client,
		prefix: prefix,
		rate:   rate,
		burst:  burst,
		ttl:    time.Minute,
		script: redis.NewScript(tokenBucketScript),
	}
}

func (l *RedisTokenBucketLimiter) Allow(ctx context.Context, key string) (bool, error) {
	result, err := l.script.Run(ctx, l.client, []string{l.prefix + key},
		time.Now().UnixMilli(),
		l.rate,
		l.burst,
		l.ttl.Milliseconds(),
		1,
	).Result()
	if err != nil {
		return false, fmt.Errorf("redis token bucket: %w", err)
	}
	values, ok := result.([]any)
	if !ok || len(values) == 0 {
		return false, fmt.Errorf("unexpected token bucket result %T", result)
	}
	allowed, ok := values[0].(int64)
	if !ok {
		return false, fmt.Errorf("unexpected token bucket allowed value %T", values[0])
	}
	return allowed == 1, nil
}

type MemoryTokenBucketLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*memoryBucket
	now     func() time.Time
}

type memoryBucket struct {
	tokens float64
	ts     time.Time
}

func NewMemoryTokenBucketLimiter(rate float64, burst int) *MemoryTokenBucketLimiter {
	if rate <= 0 {
		rate = 100
	}
	if burst <= 0 {
		burst = int(rate)
	}
	return &MemoryTokenBucketLimiter{
		rate:    rate,
		burst:   float64(burst),
		buckets: make(map[string]*memoryBucket),
		now:     time.Now,
	}
}

func (l *MemoryTokenBucketLimiter) Allow(ctx context.Context, key string) (bool, error) {
	_ = ctx
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		bucket = &memoryBucket{tokens: l.burst, ts: now}
		l.buckets[key] = bucket
	}

	elapsed := now.Sub(bucket.ts).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * l.rate
		if bucket.tokens > l.burst {
			bucket.tokens = l.burst
		}
		bucket.ts = now
	}
	if bucket.tokens < 1 {
		return false, nil
	}
	bucket.tokens--
	return true, nil
}
