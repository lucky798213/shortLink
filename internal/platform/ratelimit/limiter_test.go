package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryTokenBucketLimiter(t *testing.T) {
	limiter := NewMemoryTokenBucketLimiter(1, 2)
	now := time.Unix(100, 0)
	limiter.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(context.Background(), "ip")
		if err != nil {
			t.Fatalf("Allow() unexpected error: %v", err)
		}
		if !allowed {
			t.Fatalf("Allow() #%d = false, want true", i+1)
		}
	}

	allowed, err := limiter.Allow(context.Background(), "ip")
	if err != nil {
		t.Fatalf("Allow() unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("Allow() = true after burst exhausted, want false")
	}

	now = now.Add(time.Second)
	allowed, err = limiter.Allow(context.Background(), "ip")
	if err != nil {
		t.Fatalf("Allow() unexpected error: %v", err)
	}
	if !allowed {
		t.Fatal("Allow() = false after refill, want true")
	}
}
