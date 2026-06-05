package dao

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"short_url/pkg/generator"
	"short_url/rpc/repository"
)

func TestShortUrlRepoIntegration(t *testing.T) {
	dsn := os.Getenv("SHORT_URL_TEST_DSN")
	if dsn == "" {
		t.Skip("set SHORT_URL_TEST_DSN to run MySQL integration test")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}

	ctx := context.Background()
	repo := NewShortUrlRepo(db)
	expireAt := time.Now().Add(time.Hour).Truncate(time.Second)

	id, err := repo.Create(ctx, "https://example.com/integration", &expireAt)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	shortCode := generator.Encode(id)
	if err := repo.UpdateShortCode(ctx, id, shortCode); err != nil {
		t.Fatalf("UpdateShortCode() error: %v", err)
	}
	defer repo.DeleteByShortCode(ctx, shortCode)

	row, err := repo.FindByShortCode(ctx, shortCode)
	if err != nil {
		t.Fatalf("FindByShortCode() error: %v", err)
	}
	if row == nil {
		t.Fatal("FindByShortCode() = nil, want row")
	}
	if row.OriginURL != "https://example.com/integration" {
		t.Fatalf("OriginURL = %q, want https://example.com/integration", row.OriginURL)
	}
	if row.ExpireAt == nil {
		t.Fatal("ExpireAt = nil, want value")
	}

	deleted, err := repo.DeleteByShortCode(ctx, shortCode)
	if err != nil {
		t.Fatalf("DeleteByShortCode() error: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteByShortCode() = false, want true")
	}

	row, err = repo.FindByShortCode(ctx, shortCode)
	if err != nil {
		t.Fatalf("FindByShortCode() after delete error: %v", err)
	}
	if row != nil {
		t.Fatalf("FindByShortCode() after delete = %#v, want nil", row)
	}
}

func TestShortUrlVisitRepoIntegration(t *testing.T) {
	dsn := os.Getenv("SHORT_URL_TEST_DSN")
	if dsn == "" {
		t.Skip("set SHORT_URL_TEST_DSN to run MySQL integration test")
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect mysql: %v", err)
	}

	ctx := context.Background()
	repo := NewShortUrlVisitRepo(db)
	shortCode := "visit-it"
	now := time.Now().Truncate(time.Second)

	visits := []repository.ShortUrlVisit{
		{ShortCode: shortCode, IP: "hash-a", UserAgent: "agent-a", Referer: "referer-a", CreatedAt: now},
		{ShortCode: shortCode, IP: "hash-b", UserAgent: "agent-a", Referer: "referer-a", CreatedAt: now.Add(time.Second)},
		{ShortCode: shortCode, IP: "hash-a", UserAgent: "agent-b", Referer: "referer-b", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, visit := range visits {
		if err := repo.CreateVisit(ctx, visit); err != nil {
			t.Fatalf("CreateVisit() error: %v", err)
		}
	}
	defer db.WithContext(ctx).Exec("DELETE FROM short_url_visits WHERE short_code = ?", shortCode)

	stats, err := repo.GetStats(ctx, shortCode, 5)
	if err != nil {
		t.Fatalf("GetStats() error: %v", err)
	}
	if stats.PV != 3 {
		t.Fatalf("PV = %d, want 3", stats.PV)
	}
	if stats.UV != 2 {
		t.Fatalf("UV = %d, want 2", stats.UV)
	}
	if stats.LastVisitedAt == nil {
		t.Fatal("LastVisitedAt = nil, want value")
	}
	if len(stats.TopReferers) == 0 || stats.TopReferers[0].Value != "referer-a" || stats.TopReferers[0].Count != 2 {
		t.Fatalf("TopReferers = %#v, want referer-a count 2", stats.TopReferers)
	}
	if len(stats.TopUserAgents) == 0 || stats.TopUserAgents[0].Value != "agent-a" || stats.TopUserAgents[0].Count != 2 {
		t.Fatalf("TopUserAgents = %#v, want agent-a count 2", stats.TopUserAgents)
	}
}
