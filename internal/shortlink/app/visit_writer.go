package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"short_url/internal/shortlink"
)

type VisitWriter struct {
	store      VisitStore
	queue      chan shortlink.Visit
	ipHashSalt string
	stateMu    sync.RWMutex
	closed     bool
	workers    sync.WaitGroup
	now        func() time.Time
}

func newVisitWriter(store VisitStore, opts VisitOptions) *VisitWriter {
	if opts.QueueSize <= 0 {
		opts.QueueSize = 10000
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.IPHashSalt == "" {
		opts.IPHashSalt = "short_url_dev_salt"
	}
	writer := &VisitWriter{store: store, ipHashSalt: opts.IPHashSalt, now: time.Now}
	if store == nil {
		return writer
	}
	writer.queue = make(chan shortlink.Visit, opts.QueueSize)
	if opts.WorkerCount > 0 {
		writer.startWorkers(opts.WorkerCount, opts.BatchSize, opts.FlushInterval)
	}
	return writer
}

func (w *VisitWriter) Enqueue(shortCode string, info *shortlink.VisitInfo) {
	if w.queue == nil || info == nil {
		return
	}
	visit := shortlink.Visit{
		ShortCode: shortCode,
		IP:        w.hashIP(info.ClientIP),
		UserAgent: truncate(info.UserAgent, 512),
		Referer:   truncate(info.Referer, 1024),
		CreatedAt: w.nowTime(),
	}
	w.stateMu.RLock()
	defer w.stateMu.RUnlock()
	if w.closed {
		return
	}
	select {
	case w.queue <- visit:
	default:
		slog.Warn("short url visit queue full, dropping visit", "short_code", shortCode)
	}
}

func (w *VisitWriter) Shutdown(ctx context.Context) error {
	w.stateMu.Lock()
	if w.queue != nil && !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.stateMu.Unlock()

	done := make(chan struct{})
	go func() {
		w.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown visit workers: %w", ctx.Err())
	}
}

func (w *VisitWriter) startWorkers(workerCount int, batchSize int, flushInterval time.Duration) {
	if batchStore, ok := w.store.(BatchVisitStore); ok {
		w.startBatchWorkers(workerCount, batchSize, flushInterval, batchStore)
		return
	}
	w.workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer w.workers.Done()
			for visit := range w.queue {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := w.store.CreateVisit(ctx, visit); err != nil {
					slog.Warn("create short url visit failed", "short_code", visit.ShortCode, "err", err)
				}
				cancel()
			}
		}()
	}
}

func (w *VisitWriter) startBatchWorkers(workerCount int, batchSize int, flushInterval time.Duration, store BatchVisitStore) {
	w.workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer w.workers.Done()
			ticker := time.NewTicker(flushInterval)
			defer ticker.Stop()
			batch := make([]shortlink.Visit, 0, batchSize)
			flush := func() {
				if len(batch) == 0 {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := store.BatchCreateVisits(ctx, batch); err != nil {
					slog.Warn("batch create short url visits failed", "count", len(batch), "err", err)
				}
				cancel()
				batch = make([]shortlink.Visit, 0, batchSize)
			}
			for {
				select {
				case visit, ok := <-w.queue:
					if !ok {
						flush()
						return
					}
					batch = append(batch, visit)
					if len(batch) >= batchSize {
						flush()
					}
				case <-ticker.C:
					flush()
				}
			}
		}()
	}
}

func (w *VisitWriter) hashIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(w.ipHashSalt + ip))
	return hex.EncodeToString(sum[:])
}

func (w *VisitWriter) nowTime() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

func truncate(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}
