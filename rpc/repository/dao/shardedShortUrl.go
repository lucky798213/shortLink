package dao

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"short_url/internal/shortlink"
	"short_url/internal/shortlink/app"
	"short_url/pkg/generator"
	"short_url/pkg/sharding"
)

type shardedShortUrlRepo struct {
	db       *gorm.DB
	strategy sharding.Strategy
}

func NewShardedShortUrlRepo(db *gorm.DB, shardCount int) app.BatchLinkStore {
	return &shardedShortUrlRepo{
		db:       db,
		strategy: sharding.NewStrategy(shardCount),
	}
}

func (r *shardedShortUrlRepo) FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error) {
	table, id, err := r.strategy.TableByShortCode(shortCode)
	if err != nil {
		return nil, nil
	}

	var row shortlink.Link
	err = r.db.WithContext(ctx).
		Table(table).
		Where("id = ? AND short_code = ? AND is_deleted = 0", id, shortCode).
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("find sharded short_code %q: %w", shortCode, err)
	}
	return &row, nil
}

func (r *shardedShortUrlRepo) DeleteByShortCode(ctx context.Context, shortCode string) (bool, error) {
	table, id, err := r.strategy.TableByShortCode(shortCode)
	if err != nil {
		return false, nil
	}
	result := r.db.WithContext(ctx).
		Table(table).
		Where("id = ? AND short_code = ? AND is_deleted = 0", id, shortCode).
		Update("is_deleted", 1)
	if result.Error != nil {
		return false, fmt.Errorf("delete sharded short_code %q: %w", shortCode, result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (r *shardedShortUrlRepo) BatchCreate(ctx context.Context, rows []shortlink.CreateInput) error {
	//没有要插入的数据，直接返回成功
	if len(rows) == 0 {
		return nil
	}

	//创建一个 map，用来按表名分组。
	byTable := make(map[string][]shortlink.CreateInput)

	//遍历每一条待插入记录。
	for _, row := range rows {
		if row.ID == 0 || row.ShortCode == "" {
			return fmt.Errorf("batch create requires id and short_code")
		}

		//校验短码是否合法。
		if _, err := generator.Decode(row.ShortCode); err != nil {
			return fmt.Errorf("invalid short_code %q: %w", row.ShortCode, err)
		}

		//核心分片逻辑。
		//它先根据 ID 计算表名：
		byTable[r.strategy.TableByID(row.ID)] = append(byTable[r.strategy.TableByID(row.ID)], row)
	}

	// 同一个批次可能跨越多张分片表，必须放在同一个事务里。
	// 否则中途某张表写入失败时，前面已经写入的分片无法回滚，调用方却会收到整批失败。
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for table, tableRows := range byTable {
			if err := tx.Table(table).Create(&tableRows).Error; err != nil {
				return fmt.Errorf("batch insert %s: %w", table, err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("batch insert sharded short urls: %w", err)
	}
	return nil
}

func (r *shardedShortUrlRepo) ScanActiveShortCodes(ctx context.Context, batchSize int, fn func([]string) error) error {
	if batchSize <= 0 {
		batchSize = 1000
	}
	for _, table := range r.strategy.Tables() {
		var lastID uint64
		for {
			var rows []struct {
				ID        uint64
				ShortCode string
			}
			if err := r.db.WithContext(ctx).
				Table(table).
				Select("id, short_code").
				Where("id > ? AND is_deleted = 0 AND short_code IS NOT NULL", lastID).
				Order("id ASC").
				Limit(batchSize).
				Find(&rows).Error; err != nil {
				return fmt.Errorf("scan active short codes from %s: %w", table, err)
			}
			if len(rows) == 0 {
				break
			}
			codes := make([]string, 0, len(rows))
			for _, row := range rows {
				lastID = row.ID
				if row.ShortCode != "" {
					codes = append(codes, row.ShortCode)
				}
			}
			if len(codes) > 0 {
				if err := fn(codes); err != nil {
					return err
				}
			}
			if len(rows) < batchSize {
				break
			}
		}
	}
	return nil
}

func (r *shardedShortUrlRepo) SoftDeleteExpired(ctx context.Context, now time.Time, batchSize int) ([]string, error) {
	if batchSize <= 0 {
		batchSize = 500
	}
	deletedCodes := make([]string, 0)
	for _, table := range r.strategy.Tables() {
		var rows []struct {
			ID        uint64
			ShortCode string
		}
		if err := r.db.WithContext(ctx).
			Table(table).
			Select("id, short_code").
			Where("is_deleted = 0 AND expire_at IS NOT NULL AND expire_at <= ?", now).
			Order("id ASC").
			Limit(batchSize).
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("select expired rows from %s: %w", table, err)
		}
		if len(rows) == 0 {
			continue
		}
		ids := make([]uint64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			if row.ShortCode != "" {
				deletedCodes = append(deletedCodes, row.ShortCode)
			}
		}
		if err := r.db.WithContext(ctx).
			Table(table).
			Where("id IN ?", ids).
			Update("is_deleted", 1).Error; err != nil {
			return nil, fmt.Errorf("soft delete expired rows from %s: %w", table, err)
		}
	}
	return deletedCodes, nil
}

var _ app.BatchLinkStore = (*shardedShortUrlRepo)(nil)
var _ app.MaintenanceStore = (*shardedShortUrlRepo)(nil)
