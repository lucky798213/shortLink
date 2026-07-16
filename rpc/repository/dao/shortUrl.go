package dao

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"short_url/internal/shortlink"
	"short_url/rpc/repository"
)

// shortUrlRepo 是 ShortUrlRepo 接口的 GORM + MySQL 实现。
// 用小写开头（不导出）强制外部只能通过接口使用，保证依赖倒置。
type shortUrlRepo struct {
	db *gorm.DB
}

// NewShortUrlRepo 创建一个基于 GORM 的 ShortUrlRepo 实现。
func NewShortUrlRepo(db *gorm.DB) repository.ShortUrlRepo {
	return &shortUrlRepo{db: db}
}

// InTx 在一个数据库事务中执行短链接相关操作。
// 这样创建短链接时的 INSERT 和 UPDATE 可以作为一个整体提交或回滚。
func (r *shortUrlRepo) InTx(ctx context.Context, fn func(txRepo repository.ShortUrlRepo) error) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&shortUrlRepo{db: tx})
	})
	if err != nil {
		return fmt.Errorf("short_url transaction: %w", err)
	}
	return nil
}

// Create 向 short_urls 表插入原始链接，并返回自增主键 ID。
//
// 为什么用 Table("short_urls") 而不是 GORM 的 Model：
// 因为我们的 short_urls 表是手动建的（scripts/mysql/init.sql），
// GORM 默认会将结构体名推导为复数表名，用 Table 显式指定更安全，避免命名不一致。
//
// 为什么要用 LAST_INSERT_ID() 而不是 GORM 自动回填：
// GORM 的 Create 在 MySQL 下会自动回填主键，但依赖的表结构必须包含主键字段。
// 这里用的匿名结构体没有 ID 字段，所以通过 SELECT LAST_INSERT_ID() 显式获取。
// 这是 MySQL 会话级别的函数，在并发场景下也是安全的。
func (r *shortUrlRepo) Create(ctx context.Context, originURL string, expireAt *time.Time) (uint64, error) {
	now := time.Now()
	row := struct {
		OriginURL string
		CreatedAt time.Time
		ExpireAt  *time.Time
	}{
		OriginURL: originURL,
		CreatedAt: now,
		ExpireAt:  expireAt,
	}
	if err := r.db.WithContext(ctx).Table("short_urls").Create(&row).Error; err != nil {
		return 0, fmt.Errorf("insert short_url: %w", err)
	}
	var id uint64
	if err := r.db.WithContext(ctx).Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error; err != nil {
		return 0, fmt.Errorf("get last insert id: %w", err)
	}
	return id, nil
}

// UpdateShortCode 将生成的短码写回到对应记录。
// 这是创建短链接流程的第二步（第一步是 Create），
// 因为短码需要根据自增 ID 来生成，所以只能先插入再更新。
func (r *shortUrlRepo) UpdateShortCode(ctx context.Context, id uint64, shortCode string) error {
	err := r.db.WithContext(ctx).
		Table("short_urls").
		Where("id = ?", id).
		Update("short_code", shortCode).Error
	if err != nil {
		return fmt.Errorf("update short_code id=%d: %w", id, err)
	}
	return nil
}

func (r *shortUrlRepo) BatchCreate(ctx context.Context, rows []shortlink.CreateInput) error {
	if len(rows) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).Table("short_urls").Create(&rows).Error; err != nil {
		return fmt.Errorf("batch insert short_urls: %w", err)
	}
	return nil
}

// FindByShortCode 根据短码查询原始链接记录。
//
// 查询条件中加入 is_deleted = 0 的原因：
// 业务上采用"软删除"策略，删除的链接不会被物理删除，
// 只是标记 is_deleted=1，这样便于数据恢复和审计。
//
// 返回值语义：
// - (nil, nil)：记录不存在（包括已软删除的记录）
// - (row, nil)：查询成功
// - (nil, error)：数据库异常
func (r *shortUrlRepo) FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error) {
	var row shortlink.Link
	err := r.db.WithContext(ctx).
		Table("short_urls").
		Where("short_code = ? AND is_deleted = 0", shortCode).
		First(&row).Error
	if err != nil {
		// gorm.ErrRecordNotFound 是正常的业务结果（短码不存在），
		// 不应该当作错误向上传递，返回 nil 表示"未找到"。
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("find by short_code %q: %w", shortCode, err)
	}
	return &row, nil
}

// DeleteByShortCode 根据短码软删除短链接。
func (r *shortUrlRepo) DeleteByShortCode(ctx context.Context, shortCode string) (bool, error) {
	result := r.db.WithContext(ctx).
		Table("short_urls").
		Where("short_code = ? AND is_deleted = 0", shortCode).
		Update("is_deleted", 1)
	if result.Error != nil {
		return false, fmt.Errorf("delete by short_code %q: %w", shortCode, result.Error)
	}
	return result.RowsAffected > 0, nil
}
