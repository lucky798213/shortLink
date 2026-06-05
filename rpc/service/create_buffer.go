package service

import (
	"context"
	"fmt"
	"time"

	"short_url/pkg/generator"
	"short_url/rpc/repository"
)

type IDAllocator interface {
	NextID(ctx context.Context) (uint64, error)
}

type CreateBufferOptions struct {
	QueueSize      int
	BatchSize      int
	FlushInterval  time.Duration
	EnqueueTimeout time.Duration
	Now            func() time.Time
}

type CreateBuffer struct {
	repo           repository.ShortUrlBatchRepo
	allocator      IDAllocator
	queue          chan createBufferRequest
	batchSize      int
	flushInterval  time.Duration
	enqueueTimeout time.Duration
	now            func() time.Time
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

func NewCreateBuffer(repo repository.ShortUrlBatchRepo, allocator IDAllocator, opts CreateBufferOptions) *CreateBuffer {
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
	b := &CreateBuffer{
		repo:           repo,
		allocator:      allocator,
		queue:          make(chan createBufferRequest, opts.QueueSize),
		batchSize:      opts.BatchSize,
		flushInterval:  opts.FlushInterval,
		enqueueTimeout: opts.EnqueueTimeout,
		now:            opts.Now,
	}
	go b.run()
	return b
}

// 请求进来时不立刻写数据库，而是先塞进队列，由后台 goroutine 攒一批后批量写入；
// 但对调用方来说，它仍然像普通同步函数一样返回 shortCode 或 error。
func (b *CreateBuffer) Create(ctx context.Context, originURL string, expireAt *time.Time) (string, error) {

	//构造一个创建请求
	req := createBufferRequest{
		originURL: originURL,
		expireAt:  expireAt,
		resultCh:  make(chan createBufferResult, 1), //后台处理完后，把结果返回给当前调用方的通道
	}

	enqueueCtx, cancel := context.WithTimeout(ctx, b.enqueueTimeout)
	defer cancel()
	select {

	//b.queue 是创建请求队列。
	case b.queue <- req:

	//如果队列满了，或者一直塞不进去，超过 b.enqueueTimeout：，直接返回错误：
	case <-enqueueCtx.Done():
		return "", fmt.Errorf("create buffer enqueue timeout: %w", enqueueCtx.Err())
	}

	select {
	//等待后台处理结果。
	case result := <-req.resultCh:
		return result.shortCode, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *CreateBuffer) run() {
	//创建了一个定时器
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	//创建一个批次容器。
	//它用来临时保存从队列里取出来的创建请求。
	batch := make([]createBufferRequest, 0, b.batchSize)
	for {
		select {
		case req := <-b.queue:
			batch = append(batch, req)
			if len(batch) >= b.batchSize {
				b.flush(batch)
				batch = make([]createBufferRequest, 0, b.batchSize)
			}

		//即使请求数量还没攒够一批，也不能一直等，最多等这么久就刷一次
		//防止低流量时请求一直卡在队列里。
		case <-ticker.C:
			if len(batch) > 0 {
				b.flush(batch)
				batch = make([]createBufferRequest, 0, b.batchSize)
			}
		}
	}
}

func (b *CreateBuffer) flush(requests []createBufferRequest) {
	//创建一个 3 秒超时的 context，给这次批量处理用。
	//也就是说，下面这些操作最多执行 3 秒：
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	//个临时结构体，用来保存：
	//原始请求 req
	//给这个请求生成出来的 shortCode
	type pendingCreate struct {
		req       createBufferRequest
		shortCode string
	}

	//rows 是准备批量插入数据库的数据
	rows := make([]repository.ShortUrlCreateInput, 0, len(requests))

	//pending 是准备回传结果的数据：
	pending := make([]pendingCreate, 0, len(requests))

	//遍历这批请求
	for _, req := range requests {
		//给当前请求分配一个 ID。
		id, err := b.allocator.NextID(ctx)
		if err != nil {
			req.resultCh <- createBufferResult{err: fmt.Errorf("allocate short url id: %w", err)}
			continue
		}

		//生成短码
		shortCode := generator.Encode(id)

		//组装数据库插入行
		rows = append(rows, repository.ShortUrlCreateInput{
			ID:        id,
			ShortCode: shortCode,
			OriginURL: req.originURL,
			CreatedAt: b.now(),
			ExpireAt:  req.expireAt,
		})

		//然后记录 pending：
		pending = append(pending, pendingCreate{req: req, shortCode: shortCode})
	}

	//没有任何可插入的 row，直接返回。
	if len(rows) == 0 {
		return
	}

	//真正批量写数据库
	if err := b.repo.BatchCreate(ctx, rows); err != nil {
		for _, item := range pending {
			item.req.resultCh <- createBufferResult{err: fmt.Errorf("batch create short url: %w", err)}
		}
		return
	}

	for _, item := range pending {
		item.req.resultCh <- createBufferResult{shortCode: item.shortCode}
	}
}
