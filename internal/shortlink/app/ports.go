package app

import (
	"context"
	"errors"
	"time"

	"short_url/internal/shortlink"
)

var ErrCacheMiss = errors.New("short link cache miss")

// LinkStore 是在线请求所需的短链接持久化端口。
type LinkStore interface {
	FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error)
	DeleteByShortCode(ctx context.Context, shortCode string) (bool, error)
}

// BatchLinkStore 为创建用例提供预分配 ID 后的批量写入能力。
type BatchLinkStore interface {
	LinkStore
	BatchCreate(ctx context.Context, rows []shortlink.CreateInput) error
}

// MaintenanceStore 是后台维护任务使用的离线扫描端口。
type MaintenanceStore interface {
	ScanActiveShortCodes(ctx context.Context, batchSize int, fn func([]string) error) error
	SoftDeleteExpired(ctx context.Context, now time.Time, batchSize int) ([]string, error)
}

// VisitStore 负责访问事件持久化和统计查询。
type VisitStore interface {
	CreateVisit(ctx context.Context, visit shortlink.Visit) error
	GetStats(ctx context.Context, shortCode string, topN int) (*shortlink.Stats, error)
}

// BatchVisitStore 是支持批量写访问事件的扩展端口。
type BatchVisitStore interface {
	BatchCreateVisits(ctx context.Context, visits []shortlink.Visit) error
}

// IDAllocator 为创建用例分配全局唯一数字 ID。
type IDAllocator interface {
	NextID(ctx context.Context) (uint64, error)
}

// BloomFilter 用于在访问数据库前排除一定不存在的短码。
type BloomFilter interface {
	Add(ctx context.Context, value string) error
	Exists(ctx context.Context, value string) (bool, error)
}

// Cache 是应用层依赖的短链接缓存端口，本地缓存和 Redis 使用同一接口。
type Cache interface {
	Get(ctx context.Context, shortCode string) (*CacheEntry, error)
	Set(ctx context.Context, entry CacheEntry, ttl time.Duration) error
	Delete(ctx context.Context, shortCode string) error
}

// CacheEntry 是短链接在缓存中的稳定表示。
type CacheEntry struct {
	ShortCode string           `json:"short_code"`
	OriginURL string           `json:"origin_url"`
	CreatedAt int64            `json:"created_at"`
	ExpireAt  int64            `json:"expire_at"`
	Status    shortlink.Status `json:"status"`
}

func NewCacheEntry(link *shortlink.Link, status shortlink.Status) CacheEntry {
	entry := CacheEntry{
		ShortCode: link.ShortCode,
		OriginURL: link.OriginURL,
		CreatedAt: link.CreatedAt.Unix(),
		Status:    status,
	}
	if link.ExpireAt != nil {
		entry.ExpireAt = link.ExpireAt.Unix()
	}
	return entry
}

func (e CacheEntry) ToLink() *shortlink.Link {
	if e.ShortCode == "" && e.OriginURL == "" {
		return nil
	}
	link := &shortlink.Link{
		ShortCode: e.ShortCode,
		OriginURL: e.OriginURL,
		CreatedAt: time.Unix(e.CreatedAt, 0),
	}
	if e.ExpireAt != 0 {
		expireAt := time.Unix(e.ExpireAt, 0)
		link.ExpireAt = &expireAt
	}
	return link
}
