package proto

// 本文件由 proto/short_url.proto 的协议定义手写生成（MVP 阶段 protoc 不可用）。
// 定义了 gRPC 通信所需的消息结构体：请求和响应的数据格式。
// 如果修改了 .proto 文件，需要同步更新本文件。

// CreateShortUrlRequest 创建短链接的请求。
// OriginUrl 字段通过 JSON 标签 "origin_url" 与 HTTP 请求体的 JSON 字段对应，
// gRPC 框架会自动完成 protobuf 二进制 ↔ Go 结构体的序列化。
type CreateShortUrlRequest struct {
	OriginUrl string `json:"origin_url,omitempty"` // 用户提交的原始长链接
	ExpireAt  int64  `json:"expire_at,omitempty"`  // 过期时间（Unix 秒），0 表示不过期
}

// Reset 实现 protobuf 消息的 Reset 接口，用于对象池复用。
func (x *CreateShortUrlRequest) Reset() { *x = CreateShortUrlRequest{} }

// String 实现 fmt.Stringer 接口，方便调试时打印。
func (x *CreateShortUrlRequest) String() string { return "OriginUrl:" + x.OriginUrl }

// CreateShortUrlResponse 创建短链接的响应。
// 只返回生成的短码，前端拿到后拼接成完整短链接展示给用户。
type CreateShortUrlResponse struct {
	ShortCode string `json:"short_code,omitempty"` // 生成的 6 位短码
}

func (x *CreateShortUrlResponse) Reset()         { *x = CreateShortUrlResponse{} }
func (x *CreateShortUrlResponse) String() string { return "ShortCode:" + x.ShortCode }

// GetOriginUrlRequest 根据短码查询原始链接的请求。
type GetOriginUrlRequest struct {
	ShortCode string `json:"short_code,omitempty"` // 用户访问的短码（从 URL 路径提取）
	ClientIp  string `json:"client_ip,omitempty"`  // 访问者 IP
	UserAgent string `json:"user_agent,omitempty"` // 访问者 User-Agent
	Referer   string `json:"referer,omitempty"`    // HTTP Referer
}

func (x *GetOriginUrlRequest) Reset()         { *x = GetOriginUrlRequest{} }
func (x *GetOriginUrlRequest) String() string { return "ShortCode:" + x.ShortCode }

// GetOriginUrlResponse 查询原始链接的响应。
// CreatedAt 和 ExpireAt 使用 Unix 时间戳（int64），
// 避免跨语言/跨时区的日期格式不一致问题。
type GetOriginUrlResponse struct {
	OriginUrl string `json:"origin_url,omitempty"` // 原始长链接
	CreatedAt int64  `json:"created_at,omitempty"` // 创建时间（Unix 秒）
	ExpireAt  int64  `json:"expire_at,omitempty"`  // 过期时间（Unix 秒），0 表示不过期
	Status    string `json:"status,omitempty"`     // active / expired / not_found
}

func (x *GetOriginUrlResponse) Reset()         { *x = GetOriginUrlResponse{} }
func (x *GetOriginUrlResponse) String() string { return "OriginUrl:" + x.OriginUrl }

// GetShortUrlRequest 根据短码查询短链接详情的请求。
type GetShortUrlRequest struct {
	ShortCode string `json:"short_code,omitempty"`
}

func (x *GetShortUrlRequest) Reset()         { *x = GetShortUrlRequest{} }
func (x *GetShortUrlRequest) String() string { return "ShortCode:" + x.ShortCode }

// GetShortUrlResponse 查询短链接详情的响应。
type GetShortUrlResponse struct {
	ShortCode string `json:"short_code,omitempty"`
	OriginUrl string `json:"origin_url,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	ExpireAt  int64  `json:"expire_at,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (x *GetShortUrlResponse) Reset()         { *x = GetShortUrlResponse{} }
func (x *GetShortUrlResponse) String() string { return "ShortCode:" + x.ShortCode }

// DeleteShortUrlRequest 根据短码软删除短链接的请求。
type DeleteShortUrlRequest struct {
	ShortCode string `json:"short_code,omitempty"`
}

func (x *DeleteShortUrlRequest) Reset()         { *x = DeleteShortUrlRequest{} }
func (x *DeleteShortUrlRequest) String() string { return "ShortCode:" + x.ShortCode }

// DeleteShortUrlResponse 软删除短链接的响应。
type DeleteShortUrlResponse struct {
	Deleted bool `json:"deleted,omitempty"`
}

func (x *DeleteShortUrlResponse) Reset()         { *x = DeleteShortUrlResponse{} }
func (x *DeleteShortUrlResponse) String() string { return "DeleteShortUrlResponse" }

// StatsItem 表示统计结果中的一个 Top 项。
type StatsItem struct {
	Value string `json:"value,omitempty"`
	Count int64  `json:"count,omitempty"`
}

func (x *StatsItem) Reset()         { *x = StatsItem{} }
func (x *StatsItem) String() string { return "StatsItem" }

// GetShortUrlStatsRequest 根据短码查询访问统计。
type GetShortUrlStatsRequest struct {
	ShortCode string `json:"short_code,omitempty"`
}

func (x *GetShortUrlStatsRequest) Reset()         { *x = GetShortUrlStatsRequest{} }
func (x *GetShortUrlStatsRequest) String() string { return "ShortCode:" + x.ShortCode }

// GetShortUrlStatsResponse 查询短链接访问统计的响应。
type GetShortUrlStatsResponse struct {
	ShortCode     string       `json:"short_code,omitempty"`
	Pv            int64        `json:"pv,omitempty"`
	Uv            int64        `json:"uv,omitempty"`
	LastVisitedAt int64        `json:"last_visited_at,omitempty"`
	TopReferers   []*StatsItem `json:"top_referers,omitempty"`
	TopUserAgents []*StatsItem `json:"top_user_agents,omitempty"`
	Status        string       `json:"status,omitempty"`
}

func (x *GetShortUrlStatsResponse) Reset()         { *x = GetShortUrlStatsResponse{} }
func (x *GetShortUrlStatsResponse) String() string { return "ShortCode:" + x.ShortCode }
