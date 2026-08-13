package mysqlstore

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLIDAllocatorCachesAllocatedRange(t *testing.T) {
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

	mock.ExpectBegin()
	mock.ExpectExec("INSERT IGNORE INTO short_url_id_alloc").WithArgs(defaultAllocName, uint64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT \\* FROM `short_url_id_alloc`.*FOR UPDATE").WithArgs(defaultAllocName, 1).
		WillReturnRows(sqlmock.NewRows([]string{"name", "next_id"}).AddRow(defaultAllocName, 41))
	mock.ExpectExec("UPDATE `short_url_id_alloc` SET `next_id`=\\? WHERE name = \\?").WithArgs(uint64(44), defaultAllocName).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	allocator := NewMySQLIDAllocator(db, 3)
	for want := uint64(41); want <= 43; want++ {
		got, err := allocator.NextID(context.Background())
		if err != nil {
			t.Fatalf("NextID() error: %v", err)
		}
		if got != want {
			t.Fatalf("NextID() = %d, want %d", got, want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
}
