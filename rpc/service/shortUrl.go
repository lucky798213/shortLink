package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"

	"short_url/pkg/generator"
	"short_url/rpc/repository"
	cachepkg "short_url/rpc/repository/cache"
)

const (
	StatusActive   = "active"
	StatusExpired  = "expired"
	StatusNotFound = "not_found"
)

var ErrInvalidExpireAt = errors.New("expire_at must be in the future")

type ShortUrlResult struct {
	Row    *repository.ShortUrl
	Status string
}

type VisitInfo struct {
	ClientIP  string
	UserAgent string
	Referer   string
}

type ShortUrlStatsResult struct {
	Stats  *repository.ShortUrlStats
	Status string
}

type ShortUrlServiceOptions struct {
	RemoteCache          cachepkg.ShortUrlCache
	LocalCache           cachepkg.ShortUrlCache
	DefaultCacheTTL      time.Duration
	NotFoundCacheTTL     time.Duration
	LocalCacheTTL        time.Duration
	CacheJitterRatio     float64
	VisitRepo            repository.ShortUrlVisitRepo
	VisitQueueSize       int
	VisitWorkerCount     int
	VisitBatchSize       int
	VisitFlushInterval   time.Duration
	IPHashSalt           string
	IDAllocator          IDAllocator
	CreateQueueSize      int
	CreateBatchSize      int
	CreateFlushInterval  time.Duration
	CreateEnqueueTimeout time.Duration
	BloomFilter          BloomFilter
}

// ShortUrlService 封装短链接的核心业务逻辑。
// Service 层位于 gRPC Handler 和 Repository 之间，负责：
// 1. 编排多个 Repository 操作（如先 Create 再 UpdateShortCode）
// 2. 调用 generator 生成短码
// 3. 记录业务日志
// Handler 层只做参数转换和协议适配，不包含业务逻辑。
//
// 字段说明：
//   - repo：短链接主表的数据访问入口，负责创建、查询、软删除短链接。
//   - visitRepo：访问日志的数据访问入口，负责记录访问日志和查询 PV/UV 统计。
//   - remoteCache：远程共享缓存，通常是 Redis，多台 RPC 实例可以共享。
//   - localCache：本地进程缓存，命中速度最快，用来减少 Redis 和 MySQL 压力。
//   - defaultCacheTTL：正常短链接写入远程缓存时的默认过期时间。
//   - notFoundCacheTTL：不存在短码的空值缓存时间，用来防止缓存穿透。
//   - localCacheTTL：本地缓存过期时间，通常比远程缓存短，降低脏数据停留时间。
//   - cacheJitterRatio：远程缓存 TTL 抖动比例，避免大量 key 同时过期。
//   - visitCh：访问日志异步队列，跳转成功后先入队，再由后台 worker 落库。
//   - ipHashSalt：IP 哈希盐值，记录访问日志时对客户端 IP 做脱敏。
//   - lookupGroup：singleflight 并发合并器，同一个短码只让一个请求回源数据库。
//   - createBuffer：创建短链的批量写缓冲，开启后可减少数据库写入次数。
//   - bloomFilter：布隆过滤器，用来快速判断短码大概率不存在，减少无效查询。
//   - now：当前时间函数，生产环境用 time.Now，测试时可注入固定时间。
//   - jitter：TTL 抖动函数，测试时可替换成确定性实现。
type ShortUrlService struct {
	repo             repository.ShortUrlRepo                    // 短链接主表 Repository，负责创建、查询、软删除等核心数据操作。
	visitRepo        repository.ShortUrlVisitRepo               // 访问日志 Repository，负责写入访问记录并聚合统计数据。
	remoteCache      cachepkg.ShortUrlCache                     // 远程共享缓存，通常是 Redis，用于多个 RPC 实例之间共享短码映射。
	localCache       cachepkg.ShortUrlCache                     // 本地进程缓存，减少 Redis/数据库访问，命中后返回最快。
	defaultCacheTTL  time.Duration                              // 正常短链接写入远程缓存时的默认过期时间。
	notFoundCacheTTL time.Duration                              // 空结果缓存 TTL，用于缓存不存在的短码，防止穿透数据库。
	localCacheTTL    time.Duration                              // 本地缓存 TTL，通常比远程缓存短，降低本地脏数据停留时间。
	cacheJitterRatio float64                                    // 远程缓存 TTL 抖动比例，让大量 key 不在同一时间集中失效。
	visitCh          chan repository.ShortUrlVisit              // 异步访问日志队列，跳转成功后先入队，再由后台 worker 落库。
	ipHashSalt       string                                     // IP 哈希盐值，记录访问日志时对客户端 IP 做脱敏。
	lookupGroup      singleflight.Group                         // 并发回源合并器，同一个短码只让一个请求查数据库。
	createBuffer     *CreateBuffer                              // 创建短链的批量写缓冲；为 nil 时走普通事务写入。
	bloomFilter      BloomFilter                                // 布隆过滤器，用于快速拦截大概率不存在的短码。
	now              func() time.Time                           // 当前时间函数，生产环境用 time.Now，测试可注入固定时间。
	jitter           func(time.Duration, float64) time.Duration // TTL 抖动函数，测试时可替换为确定性实现。
}

type BloomFilter interface {
	Add(ctx context.Context, value string) error
	Exists(ctx context.Context, value string) (bool, error)
}

// NewShortUrlService 创建 ShortUrlService 实例。
func NewShortUrlService(repo repository.ShortUrlRepo) *ShortUrlService {
	return NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{})
}

func NewShortUrlServiceWithOptions(repo repository.ShortUrlRepo, opts ShortUrlServiceOptions) *ShortUrlService {
	if opts.RemoteCache == nil {
		opts.RemoteCache = cachepkg.NewNoopShortUrlCache()
	}
	if opts.LocalCache == nil {
		opts.LocalCache = cachepkg.NewNoopShortUrlCache()
	}
	if opts.DefaultCacheTTL <= 0 {
		opts.DefaultCacheTTL = 24 * time.Hour
	}
	if opts.NotFoundCacheTTL <= 0 {
		opts.NotFoundCacheTTL = time.Minute
	}
	if opts.LocalCacheTTL <= 0 {
		opts.LocalCacheTTL = 5 * time.Minute
	}
	if opts.VisitQueueSize <= 0 {
		opts.VisitQueueSize = 10000
	}
	if opts.VisitWorkerCount < 0 {
		opts.VisitWorkerCount = 0
	}
	if opts.IPHashSalt == "" {
		opts.IPHashSalt = "short_url_dev_salt"
	}
	if opts.VisitBatchSize <= 0 {
		opts.VisitBatchSize = 100
	}
	if opts.VisitFlushInterval <= 0 {
		opts.VisitFlushInterval = time.Second
	}

	svc := &ShortUrlService{
		repo:             repo,
		visitRepo:        opts.VisitRepo,
		remoteCache:      opts.RemoteCache,
		localCache:       opts.LocalCache,
		defaultCacheTTL:  opts.DefaultCacheTTL,
		notFoundCacheTTL: opts.NotFoundCacheTTL,
		localCacheTTL:    opts.LocalCacheTTL,
		cacheJitterRatio: opts.CacheJitterRatio,
		ipHashSalt:       opts.IPHashSalt,
		bloomFilter:      opts.BloomFilter,
		now:              time.Now,
		jitter:           randomJitter,
	}
	if batchRepo, ok := repo.(repository.ShortUrlBatchRepo); ok && opts.IDAllocator != nil {
		svc.createBuffer = NewCreateBuffer(batchRepo, opts.IDAllocator, CreateBufferOptions{
			QueueSize:      opts.CreateQueueSize,
			BatchSize:      opts.CreateBatchSize,
			FlushInterval:  opts.CreateFlushInterval,
			EnqueueTimeout: opts.CreateEnqueueTimeout,
			Now:            svc.nowTime,
		})
	}
	if opts.VisitRepo != nil {
		svc.visitCh = make(chan repository.ShortUrlVisit, opts.VisitQueueSize)
	}
	if svc.visitCh != nil && opts.VisitWorkerCount > 0 {
		svc.startVisitWorkers(opts.VisitWorkerCount, opts.VisitBatchSize, opts.VisitFlushInterval)
	}
	return svc
}

// CreateShortUrl 创建短链接的完整业务流程。
//
// 执行步骤：
// 1. INSERT 原始链接到数据库，获取自增 ID
// 2. 用 Base62 将 ID 编码为 6 位短码
// 3. UPDATE 将短码写回该行
//
// 为什么分三步而不是一步完成：
// 短码需要根据自增 ID 来生成，而自增 ID 只有 INSERT 之后才能拿到。
// 这是"先有鸡还是先有蛋"的问题——要生成短码需要 ID，要拿到 ID 需要先插入。
// 另一种方案是先预生成短码再插入，但这样需要处理碰撞重试，复杂度更高。
// 两步写入虽然多一次 UPDATE，但逻辑简单可靠，且 MySQL 自增 ID 天然保证了唯一性。
func (s *ShortUrlService) CreateShortUrl(ctx context.Context, originURL string, expireAtUnix int64) (string, error) {
	//把入参的Unix 时间戳解析为程序可用的时间格式（如 time.Time）
	expireAt, err := s.parseExpireAt(expireAtUnix)
	if err != nil {
		return "", err
	}

	//批量缓冲写入模式（高并发、高性能）
	if s.createBuffer != nil {
		shortCode, err := s.createBuffer.Create(ctx, originURL, expireAt)
		if err != nil {
			return "", err
		}
		s.addBloom(ctx, shortCode)
		slog.Info("short url created", "short_code", shortCode, "expire_at", expireAtUnix, "write_mode", "batch")
		return shortCode, nil
	}

	var (
		id        uint64
		shortCode string
	)

	//同步数据库事务模式（强一致性、无缓冲）低流量
	err = s.repo.InTx(ctx, func(txRepo repository.ShortUrlRepo) error {
		// 第 1 步：插入记录，获取自增 ID
		var err error
		id, err = txRepo.Create(ctx, originURL, expireAt)
		if err != nil {
			return fmt.Errorf("create short url: %w", err)
		}

		// 第 2 步：将 ID 编码为 Base62 短码
		shortCode = generator.Encode(id)

		// 第 3 步：将短码写回数据库
		if err := txRepo.UpdateShortCode(ctx, id, shortCode); err != nil {
			return fmt.Errorf("update short code: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("create short url transaction: %w", err)
	}

	// 使用结构化日志记录创建操作，便于后续排查问题。
	// slog.Info 会输出 JSON 格式的日志，包含 id 和 short_code 字段。
	slog.Info("short url created", "id", id, "short_code", shortCode, "expire_at", expireAtUnix)
	s.addBloom(ctx, shortCode)
	return shortCode, nil
}

// GetOriginUrl 根据短码查询原始链接。
// 返回 StatusNotFound 表示短码不存在（或已被软删除），调用方需自行处理 404 逻辑。
func (s *ShortUrlService) GetOriginUrl(ctx context.Context, shortCode string, visitInfo *VisitInfo) (*ShortUrlResult, error) {
	result, err := s.getShortUrl(ctx, shortCode)
	if err != nil {
		return nil, err
	}
	if result != nil && result.Status == StatusActive && result.Row != nil {
		s.enqueueVisit(result.Row.ShortCode, visitInfo)
	}
	return result, nil
}

// GetShortUrl 根据短码查询短链接详情。
func (s *ShortUrlService) GetShortUrl(ctx context.Context, shortCode string) (*ShortUrlResult, error) {
	return s.getShortUrl(ctx, shortCode)
}

// DeleteShortUrl 根据短码软删除短链接。
func (s *ShortUrlService) DeleteShortUrl(ctx context.Context, shortCode string) (bool, error) {
	s.deleteCaches(ctx, shortCode)
	deleted, err := s.repo.DeleteByShortCode(ctx, shortCode)
	s.deleteCaches(ctx, shortCode)
	if err != nil {
		return false, fmt.Errorf("delete short url: %w", err)
	}
	return deleted, nil
}

func (s *ShortUrlService) GetShortUrlStats(ctx context.Context, shortCode string) (*ShortUrlStatsResult, error) {
	result, err := s.getShortUrl(ctx, shortCode)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Status == StatusNotFound || result.Row == nil {
		return &ShortUrlStatsResult{Status: StatusNotFound}, nil
	}
	if s.visitRepo == nil {
		return &ShortUrlStatsResult{
			Status: result.Status,
			Stats:  &repository.ShortUrlStats{ShortCode: shortCode},
		}, nil
	}

	stats, err := s.visitRepo.GetStats(ctx, shortCode, 5)
	if err != nil {
		return nil, fmt.Errorf("get short url stats: %w", err)
	}
	if stats == nil {
		stats = &repository.ShortUrlStats{ShortCode: shortCode}
	}
	stats.ShortCode = shortCode
	return &ShortUrlStatsResult{
		Stats:  stats,
		Status: result.Status,
	}, nil
}

func (s *ShortUrlService) getShortUrl(ctx context.Context, shortCode string) (*ShortUrlResult, error) {
	//校验 shortCode 格式
	if !generator.IsValid(shortCode) {
		return &ShortUrlResult{Status: StatusNotFound}, nil
	}

	//查本地缓存，warmLocal = 如果当前缓存命中了，要不要顺手写入本地缓存。
	//本地缓存是最快的一层，通常在进程内存里，适合高频热点短链。命中后直接返回，不走 Redis，也不走数据库。
	if cached, ok := s.getCache(ctx, s.localCache, shortCode, false); ok {
		return cached, nil
	}

	//查远端缓存
	//远端缓存一般是 Redis。它比本地缓存慢一点，但比 MySQL 快很多，也能在多实例之间共享数据。
	if cached, ok := s.getCache(ctx, s.remoteCache, shortCode, true); ok {
		return cached, nil
	}

	//Bloom Filter 布隆过滤器
	//先判断服务有没有配置 Bloom Filter。
	if s.bloomFilter != nil {
		//问 Bloom Filter：这个 shortCode 有没有可能存在？
		exists, err := s.bloomFilter.Exists(ctx, shortCode)
		if err != nil {
			//查询失败：
			slog.Warn("short url bloom check failed, fallback to mysql", "short_code", shortCode, "err", err)
		} else if !exists {

			//构造一个不存在结果
			result := &ShortUrlResult{Status: StatusNotFound}

			//将这个不存在的短码写入缓存
			entry := cachepkg.ShortUrlEntry{
				ShortCode: shortCode,
				Status:    StatusNotFound,
			}
			s.setRemoteCache(ctx, entry, s.jitterTTL(s.notFoundCacheTTL))
			s.setLocalCache(ctx, entry, s.localCacheTTLFor(nil, StatusNotFound))
			return result, nil
		}
	}

	//singleflight 合并并发请求
	value, err, _ := s.lookupGroup.Do(shortCode, func() (any, error) {
		//再查一次 redis
		if cached, ok := s.getCache(ctx, s.remoteCache, shortCode, true); ok {
			return cached, nil
		}

		//查数据库：
		return s.loadShortUrlFromRepo(ctx, shortCode)
	})
	if err != nil {
		return nil, err
	}
	result, ok := value.(*ShortUrlResult)
	if !ok {
		return nil, fmt.Errorf("unexpected singleflight result for short_code %q", shortCode)
	}
	return result, nil
}

func (s *ShortUrlService) loadShortUrlFromRepo(ctx context.Context, shortCode string) (*ShortUrlResult, error) {
	//根据 shortCode 查数据库。
	row, err := s.repo.FindByShortCode(ctx, shortCode)
	if err != nil {
		return nil, fmt.Errorf("get origin url: %w", err)
	}

	//短码不存在
	if row == nil {
		//返回业务结果：
		result := &ShortUrlResult{Status: StatusNotFound}
		entry := cachepkg.ShortUrlEntry{
			ShortCode: shortCode,
			Status:    StatusNotFound,
		}

		//写入负缓存：
		//默认较短，比如 1 分钟。因为“不存在”也可能在之后变成“存在”，负缓存不能放太久。
		s.setRemoteCache(ctx, entry, s.jitterTTL(s.notFoundCacheTTL))
		s.setLocalCache(ctx, entry, s.localCacheTTLFor(nil, StatusNotFound))
		return result, nil
	}

	//把数据库行转换成业务结果。
	result := &ShortUrlResult{
		Row: row,
		//判断短链是否过期：
		Status: s.status(row),
	}

	//然后把数据库行转换成缓存 entry：
	entry := cachepkg.NewShortUrlEntry(row, result.Status)
	s.setRemoteCache(ctx, entry, s.cacheTTL(row, result.Status))
	s.setLocalCache(ctx, entry, s.localCacheTTLFor(row, result.Status))
	return result, nil
}

func (s *ShortUrlService) parseExpireAt(expireAtUnix int64) (*time.Time, error) {
	if expireAtUnix == 0 {
		return nil, nil
	}
	if expireAtUnix <= s.nowTime().Unix() {
		return nil, ErrInvalidExpireAt
	}
	expireAt := time.Unix(expireAtUnix, 0)
	return &expireAt, nil
}

func (s *ShortUrlService) status(row *repository.ShortUrl) string {
	if row.ExpireAt != nil && !row.ExpireAt.After(s.nowTime()) {
		return StatusExpired
	}
	return StatusActive
}

func (s *ShortUrlService) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *ShortUrlService) getCache(ctx context.Context, shortURLCache cachepkg.ShortUrlCache, shortCode string, warmLocal bool) (*ShortUrlResult, bool) {
	entry, err := shortURLCache.Get(ctx, shortCode)
	if errors.Is(err, cachepkg.ErrMiss) {
		return nil, false
	}
	if err != nil {
		slog.Warn("short url cache get failed, fallback to mysql", "short_code", shortCode, "err", err)
		return nil, false
	}
	if entry == nil {
		return nil, false
	}
	if entry.Status == StatusNotFound {
		if warmLocal {
			s.setLocalCache(ctx, *entry, s.localCacheTTLFor(nil, StatusNotFound))
		}
		return &ShortUrlResult{Status: StatusNotFound}, true
	}

	row := entry.ToRow()
	if row == nil {
		return &ShortUrlResult{Status: StatusNotFound}, true
	}
	status := entry.Status
	if status == "" {
		status = s.status(row)
	}
	if row.ExpireAt != nil && !row.ExpireAt.After(s.nowTime()) {
		status = StatusExpired
	}
	if warmLocal {
		s.setLocalCache(ctx, *entry, s.localCacheTTLFor(row, status))
	}
	return &ShortUrlResult{
		Row:    row,
		Status: status,
	}, true
}

func (s *ShortUrlService) setRemoteCache(ctx context.Context, entry cachepkg.ShortUrlEntry, ttl time.Duration) {
	if err := s.remoteCache.Set(ctx, entry, ttl); err != nil {
		slog.Warn("short url cache set failed", "short_code", entry.ShortCode, "status", entry.Status, "err", err)
	}
}

func (s *ShortUrlService) setLocalCache(ctx context.Context, entry cachepkg.ShortUrlEntry, ttl time.Duration) {
	if err := s.localCache.Set(ctx, entry, ttl); err != nil {
		slog.Warn("short url local cache set failed", "short_code", entry.ShortCode, "status", entry.Status, "err", err)
	}
}

func (s *ShortUrlService) deleteCaches(ctx context.Context, shortCode string) {
	if err := s.localCache.Delete(ctx, shortCode); err != nil {
		slog.Warn("short url local cache delete failed", "short_code", shortCode, "err", err)
	}
	if err := s.remoteCache.Delete(ctx, shortCode); err != nil {
		slog.Warn("short url cache delete failed", "short_code", shortCode, "err", err)
	}
}

func (s *ShortUrlService) addBloom(ctx context.Context, shortCode string) {
	if s.bloomFilter == nil {
		return
	}
	if err := s.bloomFilter.Add(ctx, shortCode); err != nil {
		slog.Warn("short url bloom add failed", "short_code", shortCode, "err", err)
	}
}

func (s *ShortUrlService) cacheTTL(row *repository.ShortUrl, status string) time.Duration {
	if status == StatusActive && row.ExpireAt != nil {
		ttl := row.ExpireAt.Sub(s.nowTime())
		if ttl > 0 {
			return ttl
		}
	}
	return s.jitterTTL(s.defaultCacheTTL)
}

func (s *ShortUrlService) localCacheTTLFor(row *repository.ShortUrl, status string) time.Duration {
	if status == StatusActive && row != nil && row.ExpireAt != nil {
		ttl := row.ExpireAt.Sub(s.nowTime())
		if ttl > 0 && ttl < s.localCacheTTL {
			return ttl
		}
	}
	return s.localCacheTTL
}

func (s *ShortUrlService) jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 || s.cacheJitterRatio <= 0 {
		return ttl
	}
	return s.jitter(ttl, s.cacheJitterRatio)
}

func randomJitter(ttl time.Duration, ratio float64) time.Duration {
	if ttl <= 0 || ratio <= 0 {
		return ttl
	}
	factor := 1 + ((rand.Float64()*2 - 1) * ratio)
	if factor <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * factor)
}

func (s *ShortUrlService) enqueueVisit(shortCode string, info *VisitInfo) {
	if s.visitCh == nil || info == nil {
		return
	}

	visit := repository.ShortUrlVisit{
		ShortCode: shortCode,
		IP:        s.hashIP(info.ClientIP),
		UserAgent: truncate(info.UserAgent, 512),
		Referer:   truncate(info.Referer, 1024),
		CreatedAt: s.nowTime(),
	}
	select {
	case s.visitCh <- visit:
	default:
		slog.Warn("short url visit queue full, dropping visit", "short_code", shortCode)
	}
}

func (s *ShortUrlService) startVisitWorkers(workerCount int, batchSize int, flushInterval time.Duration) {
	if batchRepo, ok := s.visitRepo.(repository.ShortUrlVisitBatchRepo); ok {
		s.startVisitBatchWorkers(workerCount, batchSize, flushInterval, batchRepo)
		return
	}
	for i := 0; i < workerCount; i++ {
		go func() {
			for visit := range s.visitCh {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := s.visitRepo.CreateVisit(ctx, visit); err != nil {
					slog.Warn("create short url visit failed", "short_code", visit.ShortCode, "err", err)
				}
				cancel()
			}
		}()
	}
}

func (s *ShortUrlService) startVisitBatchWorkers(workerCount int, batchSize int, flushInterval time.Duration, batchRepo repository.ShortUrlVisitBatchRepo) {
	if batchSize <= 0 {
		batchSize = 100
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	for i := 0; i < workerCount; i++ {
		go func() {
			ticker := time.NewTicker(flushInterval)
			defer ticker.Stop()

			batch := make([]repository.ShortUrlVisit, 0, batchSize)
			flush := func() {
				if len(batch) == 0 {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := batchRepo.BatchCreateVisits(ctx, batch); err != nil {
					slog.Warn("batch create short url visits failed", "count", len(batch), "err", err)
				}
				cancel()
				batch = make([]repository.ShortUrlVisit, 0, batchSize)
			}

			for {
				select {
				case visit := <-s.visitCh:
					batch = append(batch, visit)
					if len(batch) >= batchSize {
						flush()
					}
				case <-ticker.C:
					flush()
				}
			}
		}()
	}
}

func (s *ShortUrlService) hashIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s.ipHashSalt + ip))
	return hex.EncodeToString(sum[:])
}

func truncate(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}
