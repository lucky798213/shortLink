package service

import (
	"context"
	"testing"
	"time"

	"short_url/internal/shortlink"
)

type lifecycleVisitRepo struct {
	batches chan []shortlink.Visit
}

func (r *lifecycleVisitRepo) CreateVisit(context.Context, shortlink.Visit) error {
	return nil
}

func (r *lifecycleVisitRepo) GetStats(context.Context, string, int) (*shortlink.Stats, error) {
	return nil, nil
}

func (r *lifecycleVisitRepo) BatchCreateVisits(_ context.Context, visits []shortlink.Visit) error {
	batch := append([]shortlink.Visit(nil), visits...)
	r.batches <- batch
	return nil
}

func TestShortUrlServiceShutdownFlushesVisitBatch(t *testing.T) {
	visitRepo := &lifecycleVisitRepo{batches: make(chan []shortlink.Visit, 1)}
	svc := NewShortUrlServiceWithOptions(&mockShortUrlRepo{}, ShortUrlServiceOptions{
		VisitRepo:          visitRepo,
		VisitQueueSize:     10,
		VisitWorkerCount:   1,
		VisitBatchSize:     100,
		VisitFlushInterval: time.Hour,
	})
	svc.enqueueVisit("000001", &VisitInfo{ClientIP: "192.0.2.1"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}

	select {
	case batch := <-visitRepo.batches:
		if len(batch) != 1 || batch[0].ShortCode != "000001" {
			t.Fatalf("visit batch = %#v, want one visit for 000001", batch)
		}
	default:
		t.Fatal("Shutdown() did not flush pending visit batch")
	}
}
