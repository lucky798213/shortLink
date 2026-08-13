package jobs

import (
	"context"
	"sync"
	"testing"
	"time"

	"short_url/internal/shortlink/app"
)

func TestExpiredCleanerDeletesCacheForExpiredLinks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &maintenanceStoreStub{expiredCodes: []string{"000001", "000002"}}
	cache := &cacheStub{deleted: make(chan string, 2)}
	StartExpiredCleaner(ctx, store, cache, ExpiredCleanerOptions{Interval: time.Millisecond, BatchSize: 10})

	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case code := <-cache.deleted:
			got[code] = true
		case <-time.After(time.Second):
			t.Fatalf("deleted codes = %#v, want both expired codes", got)
		}
	}
	cancel()
	if !got["000001"] || !got["000002"] {
		t.Fatalf("deleted codes = %#v", got)
	}
}

func TestBloomRebuilderScansAndAddsCodesImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &maintenanceStoreStub{activeCodes: []string{"000001", "000002"}}
	filter := &bloomStub{added: make(chan []string, 1)}
	StartBloomRebuilder(ctx, store, filter, BloomRebuilderOptions{Interval: time.Hour, BatchSize: 10})

	select {
	case codes := <-filter.added:
		cancel()
		if len(codes) != 2 || codes[0] != "000001" || codes[1] != "000002" {
			t.Fatalf("added codes = %#v", codes)
		}
	case <-time.After(time.Second):
		t.Fatal("bloom rebuild did not run immediately")
	}
}

type maintenanceStoreStub struct {
	mu           sync.Mutex
	expiredCodes []string
	activeCodes  []string
}

func (s *maintenanceStoreStub) ScanActiveShortCodes(ctx context.Context, batchSize int, fn func([]string) error) error {
	s.mu.Lock()
	codes := append([]string(nil), s.activeCodes...)
	s.mu.Unlock()
	return fn(codes)
}

func (s *maintenanceStoreStub) SoftDeleteExpired(context.Context, time.Time, int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	codes := append([]string(nil), s.expiredCodes...)
	s.expiredCodes = nil
	return codes, nil
}

type cacheStub struct {
	deleted chan string
}

var _ app.Cache = (*cacheStub)(nil)

func (c *cacheStub) Get(context.Context, string) (*app.CacheEntry, error) {
	return nil, app.ErrCacheMiss
}

func (c *cacheStub) Set(context.Context, app.CacheEntry, time.Duration) error {
	return nil
}

func (c *cacheStub) Delete(ctx context.Context, code string) error {
	select {
	case c.deleted <- code:
	case <-ctx.Done():
	}
	return nil
}

type bloomStub struct {
	added chan []string
}

func (f *bloomStub) Add(context.Context, string) error {
	return nil
}

func (f *bloomStub) AddMany(ctx context.Context, codes []string) error {
	copyOfCodes := append([]string(nil), codes...)
	select {
	case f.added <- copyOfCodes:
	case <-ctx.Done():
	}
	return nil
}

func (f *bloomStub) Exists(context.Context, string) (bool, error) {
	return true, nil
}
