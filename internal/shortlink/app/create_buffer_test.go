package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCreateBufferShutdownDrainsPendingBatch(t *testing.T) {
	repo := &mockBatchShortUrlRepo{}
	buffer := NewCreateBuffer(repo, &mockIDAllocator{nextID: 1}, CreateBufferOptions{
		BatchSize:     100,
		FlushInterval: time.Hour,
	})

	resultCh := make(chan createBufferResult, 1)
	buffer.queue <- createBufferRequest{
		originURL: "https://example.com",
		resultCh:  resultCh,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := buffer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}

	result := <-resultCh
	if result.err != nil || result.shortCode != "000001" {
		t.Fatalf("shutdown result = %#v, want short code 000001", result)
	}
	if len(repo.batchRows) != 1 || repo.batchRows[0].OriginURL != "https://example.com" {
		t.Fatalf("batch rows = %#v, want one drained row", repo.batchRows)
	}
}

func TestCreateBufferRejectsCreateAfterShutdown(t *testing.T) {
	buffer := NewCreateBuffer(&mockBatchShortUrlRepo{}, &mockIDAllocator{nextID: 1}, CreateBufferOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := buffer.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() unexpected error: %v", err)
	}

	_, err := buffer.Create(context.Background(), "https://example.com", nil)
	if !errors.Is(err, ErrCreateBufferClosed) {
		t.Fatalf("Create() error = %v, want ErrCreateBufferClosed", err)
	}
}
