package mysqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"short_url/internal/infra/mysql/sharding"
	"short_url/internal/shortlink"
)

func TestShardedShortUrlRepoBatchCreateCommitsAllShards(t *testing.T) {
	repo, mock := newMockShardedShortURLRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `short_urls_(01|02)`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `short_urls_(01|02)`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.BatchCreate(context.Background(), batchRowsForTwoShards())
	if err != nil {
		t.Fatalf("BatchCreate() unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func TestShardedShortUrlRepoBatchCreateRollsBackAllShards(t *testing.T) {
	repo, mock := newMockShardedShortURLRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `short_urls_(01|02)`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `short_urls_(01|02)`").WillReturnError(errors.New("insert failed"))
	mock.ExpectRollback()

	err := repo.BatchCreate(context.Background(), batchRowsForTwoShards())
	if err == nil {
		t.Fatal("BatchCreate() error = nil, want rollback error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}

func newMockShardedShortURLRepo(t *testing.T) (*ShardedLinkStore, sqlmock.Sqlmock) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true})
	if err != nil {
		t.Fatalf("gorm.Open() error: %v", err)
	}

	return &ShardedLinkStore{
		db:       db,
		strategy: sharding.NewStrategy(64),
	}, mock
}

func batchRowsForTwoShards() []shortlink.CreateInput {
	now := time.Unix(100, 0)
	return []shortlink.CreateInput{
		{ID: 1, ShortCode: "000001", OriginURL: "https://example.com/1", CreatedAt: now},
		{ID: 2, ShortCode: "000002", OriginURL: "https://example.com/2", CreatedAt: now},
	}
}
