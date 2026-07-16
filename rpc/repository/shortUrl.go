package repository

import (
	"context"
	"time"

	"short_url/internal/shortlink"
)

// ShortUrlRepo 定义短链接的数据访问接口。
//
// 为什么要定义接口而不是直接用 DAO：
// 1. 方便单元测试：可以 mock 掉数据库调用
// 2. 方便切换实现：将来可以换成 Redis、MongoDB 等存储
// 3. 依赖倒置：Service 层只依赖抽象接口，不依赖具体的 GORM 实现
type ShortUrlRepo interface {
	// InTx 在一个数据库事务中执行 fn。
	// fn 内部拿到的 txRepo 与外层 Repo 具有相同接口，但所有写操作共享同一个事务。
	InTx(ctx context.Context, fn func(txRepo ShortUrlRepo) error) error

	// Create 插入一条原始链接记录，返回数据库生成的自增 ID。
	Create(ctx context.Context, originURL string, expireAt *time.Time) (uint64, error)

	// UpdateShortCode 将生成的短码写回到对应 ID 的记录。
	// 分两步操作（先 Create 再 UpdateShortCode）的原因是：
	// 短码由自增 ID 编码而来，必须先拿到 ID 才能生成短码。
	UpdateShortCode(ctx context.Context, id uint64, shortCode string) error

	// FindByShortCode 根据短码查询完整记录。
	// 返回 nil, nil 表示记录不存在（不是错误）。
	FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error)

	// DeleteByShortCode 根据短码软删除记录。
	// 返回 false, nil 表示记录不存在或已删除。
	DeleteByShortCode(ctx context.Context, shortCode string) (bool, error)
}

// ShortUrlBatchRepo 是支持预生成 ID 后直接批量写入的扩展接口。
type ShortUrlBatchRepo interface {
	BatchCreate(ctx context.Context, rows []shortlink.CreateInput) error
}

// ShortUrlMaintenanceRepo 是后台维护任务需要的扩展接口。
type ShortUrlMaintenanceRepo interface {
	ScanActiveShortCodes(ctx context.Context, batchSize int, fn func([]string) error) error
	SoftDeleteExpired(ctx context.Context, now time.Time, batchSize int) ([]string, error)
}

// ShortUrlVisitRepo 定义短链接访问日志的数据访问接口。
type ShortUrlVisitRepo interface {
	CreateVisit(ctx context.Context, visit shortlink.Visit) error
	GetStats(ctx context.Context, shortCode string, topN int) (*shortlink.Stats, error)
}

// ShortUrlVisitBatchRepo 是访问日志批量写入扩展接口。
type ShortUrlVisitBatchRepo interface {
	BatchCreateVisits(ctx context.Context, visits []shortlink.Visit) error
}
