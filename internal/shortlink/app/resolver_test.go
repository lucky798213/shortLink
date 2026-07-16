package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"short_url/internal/shortlink"
)

func TestResolverCanceledCallerDoesNotCancelSharedLookup(t *testing.T) {
	store := &blockingLinkStore{
		started:  make(chan struct{}),
		check:    make(chan struct{}),
		checked:  make(chan bool, 1),
		release:  make(chan struct{}),
		finished: make(chan error, 1),
		link: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			CreatedAt: time.Unix(100, 0),
		},
	}
	resolver := newResolver(store, noopCache{}, noopCache{}, nil, CacheOptions{LookupTimeout: time.Second})

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(leaderCtx, "000001")
		leaderErr <- err
	}()
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("shared lookup did not start")
	}

	cancelLeader()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context canceled", err)
	}
	close(store.check)
	if alive := <-store.checked; !alive {
		t.Fatal("caller cancellation propagated to shared lookup")
	}
	close(store.release)
	if err := <-store.finished; err != nil {
		t.Fatalf("shared lookup error = %v", err)
	}
	if count := store.count.Load(); count != 1 {
		t.Fatalf("store lookup count = %d, want 1", count)
	}
}

type blockingLinkStore struct {
	started  chan struct{}
	check    chan struct{}
	checked  chan bool
	release  chan struct{}
	finished chan error
	link     *shortlink.Link
	count    atomic.Int32
	once     sync.Once
}

func (s *blockingLinkStore) FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error) {
	s.count.Add(1)
	s.once.Do(func() { close(s.started) })
	<-s.check
	select {
	case <-ctx.Done():
		s.checked <- false
		s.finished <- ctx.Err()
		return nil, ctx.Err()
	default:
		s.checked <- true
	}
	select {
	case <-s.release:
		s.finished <- nil
		return s.link, nil
	case <-ctx.Done():
		s.finished <- ctx.Err()
		return nil, ctx.Err()
	}
}

func (s *blockingLinkStore) DeleteByShortCode(context.Context, string) (bool, error) {
	return false, nil
}
