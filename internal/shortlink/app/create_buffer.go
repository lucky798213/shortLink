package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"short_url/internal/shortlink"
	"short_url/pkg/generator"
)

var ErrCreateBufferClosed = errors.New("create buffer is closed")

type CreateBufferOptions struct {
	QueueSize      int
	BatchSize      int
	FlushInterval  time.Duration
	EnqueueTimeout time.Duration
	Now            func() time.Time
}

type CreateBuffer struct {
	store          BatchLinkStore
	allocator      IDAllocator
	queue          chan createBufferRequest
	batchSize      int
	flushInterval  time.Duration
	enqueueTimeout time.Duration
	now            func() time.Time

	stateMu sync.RWMutex
	closed  bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

type createBufferRequest struct {
	originURL string
	expireAt  *time.Time
	resultCh  chan createBufferResult
}

type createBufferResult struct {
	shortCode string
	err       error
}

func NewCreateBuffer(store BatchLinkStore, allocator IDAllocator, opts CreateBufferOptions) *CreateBuffer {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 10000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 128
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 10 * time.Millisecond
	}
	if opts.EnqueueTimeout <= 0 {
		opts.EnqueueTimeout = 200 * time.Millisecond
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	buffer := &CreateBuffer{
		store:          store,
		allocator:      allocator,
		queue:          make(chan createBufferRequest, opts.QueueSize),
		batchSize:      opts.BatchSize,
		flushInterval:  opts.FlushInterval,
		enqueueTimeout: opts.EnqueueTimeout,
		now:            opts.Now,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}
	go buffer.run()
	return buffer
}

func (b *CreateBuffer) Create(ctx context.Context, originURL string, expireAt *time.Time) (string, error) {
	request := createBufferRequest{
		originURL: originURL,
		expireAt:  expireAt,
		resultCh:  make(chan createBufferResult, 1),
	}
	enqueueCtx, cancel := context.WithTimeout(ctx, b.enqueueTimeout)
	defer cancel()

	b.stateMu.RLock()
	if b.closed {
		b.stateMu.RUnlock()
		return "", ErrCreateBufferClosed
	}
	select {
	case b.queue <- request:
		b.stateMu.RUnlock()
	case <-enqueueCtx.Done():
		b.stateMu.RUnlock()
		return "", fmt.Errorf("create buffer enqueue timeout: %w", enqueueCtx.Err())
	}

	select {
	case result := <-request.resultCh:
		return result.shortCode, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *CreateBuffer) Shutdown(ctx context.Context) error {
	b.stateMu.Lock()
	if !b.closed {
		b.closed = true
		close(b.stopCh)
	}
	b.stateMu.Unlock()

	select {
	case <-b.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *CreateBuffer) run() {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	defer close(b.doneCh)

	batch := make([]createBufferRequest, 0, b.batchSize)
	for {
		select {
		case request := <-b.queue:
			batch = append(batch, request)
			if len(batch) >= b.batchSize {
				b.flush(batch)
				batch = make([]createBufferRequest, 0, b.batchSize)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				b.flush(batch)
				batch = make([]createBufferRequest, 0, b.batchSize)
			}
		case <-b.stopCh:
			for {
				select {
				case request := <-b.queue:
					batch = append(batch, request)
				default:
					if len(batch) > 0 {
						b.flush(batch)
					}
					return
				}
			}
		}
	}
}

func (b *CreateBuffer) flush(requests []createBufferRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type pendingCreate struct {
		request   createBufferRequest
		shortCode string
	}
	rows := make([]shortlink.CreateInput, 0, len(requests))
	pending := make([]pendingCreate, 0, len(requests))
	for _, request := range requests {
		id, err := b.allocator.NextID(ctx)
		if err != nil {
			request.resultCh <- createBufferResult{err: fmt.Errorf("allocate short url id: %w", err)}
			continue
		}
		if id == 0 || id > generator.MaxID {
			request.resultCh <- createBufferResult{err: fmt.Errorf("allocate short url id: invalid id %d", id)}
			continue
		}
		shortCode := generator.Encode(id)
		rows = append(rows, shortlink.CreateInput{
			ID:        id,
			ShortCode: shortCode,
			OriginURL: request.originURL,
			CreatedAt: b.now(),
			ExpireAt:  request.expireAt,
		})
		pending = append(pending, pendingCreate{request: request, shortCode: shortCode})
	}
	if len(rows) == 0 {
		return
	}
	if err := b.store.BatchCreate(ctx, rows); err != nil {
		for _, item := range pending {
			item.request.resultCh <- createBufferResult{err: fmt.Errorf("batch create short url: %w", err)}
		}
		return
	}
	for _, item := range pending {
		item.request.resultCh <- createBufferResult{shortCode: item.shortCode}
	}
}
