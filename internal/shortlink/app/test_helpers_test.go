package app

import (
	"time"

	"short_url/internal/shortlink"
)

const (
	StatusActive   = shortlink.StatusActive
	StatusExpired  = shortlink.StatusExpired
	StatusNotFound = shortlink.StatusNotFound
)

var ErrInvalidExpireAt = shortlink.ErrInvalidExpireAt

type VisitInfo = shortlink.VisitInfo
type ShortUrlService = Service

type ShortUrlServiceOptions struct {
	RemoteCache          Cache
	LocalCache           Cache
	DefaultCacheTTL      time.Duration
	NotFoundCacheTTL     time.Duration
	LocalCacheTTL        time.Duration
	CacheJitterRatio     float64
	VisitRepo            VisitStore
	VisitQueueSize       int
	VisitWorkerCount     int
	VisitBatchSize       int
	VisitFlushInterval   time.Duration
	IPHashSalt           string
	IDAllocator          IDAllocator
	CreateQueueSize      int
	CreateBatchSize      int
	CreateFlushInterval  time.Duration
	CreateEnqueueTimeout time.Duration
	BloomFilter          BloomFilter
}

func NewShortUrlService(store BatchLinkStore) *Service {
	allocator := IDAllocator(&mockIDAllocator{nextID: 1})
	if mock, ok := store.(*mockShortUrlRepo); ok && mock.createID > 0 {
		allocator = &mockIDAllocator{nextID: mock.createID}
	}
	return NewService(store, Options{IDAllocator: allocator})
}

func NewShortUrlServiceWithOptions(store BatchLinkStore, opts ShortUrlServiceOptions) *Service {
	return NewService(store, Options{
		RemoteCache: opts.RemoteCache,
		LocalCache:  opts.LocalCache,
		Cache: CacheOptions{
			DefaultTTL:  opts.DefaultCacheTTL,
			NotFoundTTL: opts.NotFoundCacheTTL,
			LocalTTL:    opts.LocalCacheTTL,
			JitterRatio: opts.CacheJitterRatio,
		},
		IDAllocator: opts.IDAllocator,
		Create: CreateOptions{
			Buffered:       opts.IDAllocator != nil,
			QueueSize:      opts.CreateQueueSize,
			BatchSize:      opts.CreateBatchSize,
			FlushInterval:  opts.CreateFlushInterval,
			EnqueueTimeout: opts.CreateEnqueueTimeout,
		},
		BloomFilter: opts.BloomFilter,
		VisitStore:  opts.VisitRepo,
		Visit: VisitOptions{
			QueueSize:     opts.VisitQueueSize,
			WorkerCount:   opts.VisitWorkerCount,
			BatchSize:     opts.VisitBatchSize,
			FlushInterval: opts.VisitFlushInterval,
			IPHashSalt:    opts.IPHashSalt,
		},
	})
}

func setServiceNow(service *Service, now func() time.Time) {
	service.creator.now = now
	service.resolver.now = now
	service.visitWriter.now = now
}
