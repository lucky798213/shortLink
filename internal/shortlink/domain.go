package shortlink

import "time"

// Status 表示短链接对外呈现的业务状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusExpired  Status = "expired"
	StatusNotFound Status = "not_found"
)

// Link 是短链接领域模型，不包含具体数据库或传输协议的实现细节。
type Link struct {
	ID        uint64
	ShortCode string
	OriginURL string
	CreatedAt time.Time
	ExpireAt  *time.Time
	IsDeleted bool
}

// StatusAt 根据指定时间计算短链接状态，便于测试和统一过期语义。
func (l Link) StatusAt(now time.Time) Status {
	if l.IsDeleted {
		return StatusNotFound
	}
	if l.ExpireAt != nil && !l.ExpireAt.After(now) {
		return StatusExpired
	}
	return StatusActive
}

// CreateInput 表示持久化一条预分配 ID 的短链接所需的数据。
type CreateInput struct {
	ID        uint64
	ShortCode string
	OriginURL string
	CreatedAt time.Time
	ExpireAt  *time.Time
}

// Visit 表示一次成功跳转产生的访问事件。
type Visit struct {
	ID        uint64
	ShortCode string
	IP        string
	UserAgent string
	Referer   string
	CreatedAt time.Time
}

// VisitInfo 是跳转入口采集的原始访问元数据。
type VisitInfo struct {
	ClientIP  string
	UserAgent string
	Referer   string
}

// StatsItem 表示统计结果中的一个排名项。
type StatsItem struct {
	Value string
	Count int64
}

// Stats 是短链接访问统计的领域结果。
type Stats struct {
	ShortCode     string
	PV            int64
	UV            int64
	LastVisitedAt *time.Time
	TopReferers   []StatsItem
	TopUserAgents []StatsItem
}
