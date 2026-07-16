package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"short_url/internal/shortlink"
	"short_url/rpc/repository"
	cachepkg "short_url/rpc/repository/cache"
)

type mockShortUrlRepo struct {
	mu sync.Mutex

	inTxCalled bool

	createID  uint64
	createErr error
	updateErr error
	deleteErr error

	createCalled bool
	updateCalled bool
	findCalled   bool
	deleteCalled bool

	originURL       string
	expireAt        *time.Time
	updateID        uint64
	updateShortCode string
	findShortCode   string
	deleteShortCode string

	findRow *shortlink.Link
	deleted bool

	findDelay time.Duration
	findCount int
}

var _ repository.ShortUrlRepo = (*mockShortUrlRepo)(nil)

func (m *mockShortUrlRepo) InTx(ctx context.Context, fn func(txRepo repository.ShortUrlRepo) error) error {
	m.inTxCalled = true
	return fn(m)
}

func (m *mockShortUrlRepo) Create(ctx context.Context, originURL string, expireAt *time.Time) (uint64, error) {
	m.createCalled = true
	m.originURL = originURL
	m.expireAt = expireAt
	if m.createErr != nil {
		return 0, m.createErr
	}
	return m.createID, nil
}

func (m *mockShortUrlRepo) UpdateShortCode(ctx context.Context, id uint64, shortCode string) error {
	m.updateCalled = true
	m.updateID = id
	m.updateShortCode = shortCode
	return m.updateErr
}

func (m *mockShortUrlRepo) FindByShortCode(ctx context.Context, shortCode string) (*shortlink.Link, error) {
	m.mu.Lock()
	m.findCalled = true
	m.findCount++
	m.findShortCode = shortCode
	m.mu.Unlock()
	if m.findDelay > 0 {
		time.Sleep(m.findDelay)
	}
	return m.findRow, nil
}

func (m *mockShortUrlRepo) DeleteByShortCode(ctx context.Context, shortCode string) (bool, error) {
	m.deleteCalled = true
	m.deleteShortCode = shortCode
	return m.deleted, m.deleteErr
}

type mockShortUrlCache struct {
	mu sync.Mutex

	getEntry *cachepkg.ShortUrlEntry
	getErr   error
	setErr   error
	delErr   error

	getCalled    bool
	setCalled    bool
	deleteCalled bool
	deleteCount  int

	getShortCode    string
	setEntry        cachepkg.ShortUrlEntry
	setTTL          time.Duration
	deleteShortCode string
}

var _ cachepkg.ShortUrlCache = (*mockShortUrlCache)(nil)

func (m *mockShortUrlCache) Get(ctx context.Context, shortCode string) (*cachepkg.ShortUrlEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalled = true
	m.getShortCode = shortCode
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.getEntry, nil
}

func (m *mockShortUrlCache) Set(ctx context.Context, entry cachepkg.ShortUrlEntry, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setCalled = true
	m.setEntry = entry
	m.setTTL = ttl
	return m.setErr
}

func (m *mockShortUrlCache) Delete(ctx context.Context, shortCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalled = true
	m.deleteCount++
	m.deleteShortCode = shortCode
	return m.delErr
}

func newShortUrlServiceWithRemoteCache(repo repository.ShortUrlRepo, remoteCache cachepkg.ShortUrlCache, defaultTTL time.Duration, notFoundTTL time.Duration) *ShortUrlService {
	return NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache:      remoteCache,
		DefaultCacheTTL:  defaultTTL,
		NotFoundCacheTTL: notFoundTTL,
	})
}

type mockVisitRepo struct {
	createErr error
	statsErr  error
	stats     *shortlink.Stats

	createCalled bool
	statsCalled  bool
	visit        shortlink.Visit
	statsCode    string
}

var _ repository.ShortUrlVisitRepo = (*mockVisitRepo)(nil)

func (m *mockVisitRepo) CreateVisit(ctx context.Context, visit shortlink.Visit) error {
	m.createCalled = true
	m.visit = visit
	return m.createErr
}

func (m *mockVisitRepo) GetStats(ctx context.Context, shortCode string, topN int) (*shortlink.Stats, error) {
	m.statsCalled = true
	m.statsCode = shortCode
	return m.stats, m.statsErr
}

type mockIDAllocator struct {
	nextID uint64
	err    error
}

func (m *mockIDAllocator) NextID(ctx context.Context) (uint64, error) {
	if m.err != nil {
		return 0, m.err
	}
	if m.nextID == 0 {
		m.nextID = 1
	}
	id := m.nextID
	m.nextID++
	return id, nil
}

type mockBatchShortUrlRepo struct {
	mockShortUrlRepo
	batchRows []shortlink.CreateInput
	batchErr  error
}

func (m *mockBatchShortUrlRepo) BatchCreate(ctx context.Context, rows []shortlink.CreateInput) error {
	m.batchRows = append(m.batchRows, rows...)
	return m.batchErr
}

type mockBloomFilter struct {
	exists    bool
	existsErr error
	addErr    error

	addCalled    bool
	existsCalled bool
	addCode      string
	existsCode   string
}

func (m *mockBloomFilter) Add(ctx context.Context, value string) error {
	m.addCalled = true
	m.addCode = value
	return m.addErr
}

func (m *mockBloomFilter) Exists(ctx context.Context, value string) (bool, error) {
	m.existsCalled = true
	m.existsCode = value
	return m.exists, m.existsErr
}

func TestCreateShortUrlUsesTransaction(t *testing.T) {
	repo := &mockShortUrlRepo{createID: 62}
	svc := NewShortUrlService(repo)

	got, err := svc.CreateShortUrl(context.Background(), "https://example.com", 0)
	if err != nil {
		t.Fatalf("CreateShortUrl() unexpected error: %v", err)
	}

	if got != "000010" {
		t.Fatalf("CreateShortUrl() = %q, want %q", got, "000010")
	}
	if !repo.inTxCalled {
		t.Fatal("CreateShortUrl() did not execute in a transaction")
	}
	if !repo.createCalled {
		t.Fatal("CreateShortUrl() did not create row")
	}
	if !repo.updateCalled {
		t.Fatal("CreateShortUrl() did not update short code")
	}
	if repo.originURL != "https://example.com" {
		t.Fatalf("originURL = %q, want %q", repo.originURL, "https://example.com")
	}
	if repo.expireAt != nil {
		t.Fatalf("expireAt = %v, want nil", repo.expireAt)
	}
	if repo.updateID != 62 || repo.updateShortCode != "000010" {
		t.Fatalf("UpdateShortCode() got id=%d shortCode=%q, want id=62 shortCode=000010", repo.updateID, repo.updateShortCode)
	}
}

func TestCreateShortUrlWithFutureExpireAt(t *testing.T) {
	repo := &mockShortUrlRepo{createID: 1}
	svc := NewShortUrlService(repo)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.CreateShortUrl(context.Background(), "https://example.com", 200)
	if err != nil {
		t.Fatalf("CreateShortUrl() unexpected error: %v", err)
	}
	if got != "000001" {
		t.Fatalf("CreateShortUrl() = %q, want 000001", got)
	}
	if repo.expireAt == nil {
		t.Fatal("expireAt = nil, want time")
	}
	if repo.expireAt.Unix() != 200 {
		t.Fatalf("expireAt = %d, want 200", repo.expireAt.Unix())
	}
}

func TestCreateShortUrlRejectsPastExpireAt(t *testing.T) {
	repo := &mockShortUrlRepo{createID: 1}
	svc := NewShortUrlService(repo)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	_, err := svc.CreateShortUrl(context.Background(), "https://example.com", 100)
	if !errors.Is(err, ErrInvalidExpireAt) {
		t.Fatalf("CreateShortUrl() error = %v, want ErrInvalidExpireAt", err)
	}
	if repo.inTxCalled || repo.createCalled || repo.updateCalled {
		t.Fatalf("repo should not be called for invalid expire_at: inTx=%v create=%v update=%v", repo.inTxCalled, repo.createCalled, repo.updateCalled)
	}
}

func TestCreateShortUrlRejectsInvalidOriginURL(t *testing.T) {
	repo := &mockShortUrlRepo{createID: 1}
	svc := NewShortUrlService(repo)

	_, err := svc.CreateShortUrl(context.Background(), "ftp://example.com", 0)
	if !errors.Is(err, shortlink.ErrInvalidOriginURL) {
		t.Fatalf("CreateShortUrl() error = %v, want ErrInvalidOriginURL", err)
	}
	if repo.createCalled {
		t.Fatal("CreateShortUrl() should reject invalid URL before repository call")
	}
}

func TestCreateShortUrlReturnsUpdateError(t *testing.T) {
	repo := &mockShortUrlRepo{
		createID:  1,
		updateErr: errors.New("db update failed"),
	}
	svc := NewShortUrlService(repo)

	_, err := svc.CreateShortUrl(context.Background(), "https://example.com", 0)
	if err == nil {
		t.Fatal("CreateShortUrl() expected error")
	}
	if !strings.Contains(err.Error(), "update short code") {
		t.Fatalf("CreateShortUrl() error = %q, want update short code context", err.Error())
	}
	if !repo.inTxCalled {
		t.Fatal("CreateShortUrl() did not execute in a transaction")
	}
	if !repo.createCalled || !repo.updateCalled {
		t.Fatalf("createCalled=%v updateCalled=%v, want both true", repo.createCalled, repo.updateCalled)
	}
}

func TestCreateShortUrlUsesBatchCreateBuffer(t *testing.T) {
	repo := &mockBatchShortUrlRepo{}
	bloom := &mockBloomFilter{}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		IDAllocator:          &mockIDAllocator{nextID: 1},
		CreateQueueSize:      2,
		CreateBatchSize:      1,
		CreateFlushInterval:  time.Millisecond,
		CreateEnqueueTimeout: time.Second,
		BloomFilter:          bloom,
	})
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.CreateShortUrl(context.Background(), "https://example.com", 0)
	if err != nil {
		t.Fatalf("CreateShortUrl() unexpected error: %v", err)
	}
	if got != "000001" {
		t.Fatalf("CreateShortUrl() = %q, want 000001", got)
	}
	if len(repo.batchRows) != 1 {
		t.Fatalf("batchRows len = %d, want 1", len(repo.batchRows))
	}
	row := repo.batchRows[0]
	if row.ID != 1 || row.ShortCode != "000001" || row.OriginURL != "https://example.com" {
		t.Fatalf("batch row = %#v, want id=1 code=000001 origin URL", row)
	}
	if repo.inTxCalled || repo.createCalled || repo.updateCalled {
		t.Fatal("legacy create transaction path should not be used when create buffer is enabled")
	}
	if !bloom.addCalled || bloom.addCode != "000001" {
		t.Fatalf("bloom add called=%v code=%q, want 000001", bloom.addCalled, bloom.addCode)
	}
}

func TestCreateShortUrlBatchCreateError(t *testing.T) {
	repo := &mockBatchShortUrlRepo{batchErr: errors.New("db down")}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		IDAllocator:          &mockIDAllocator{nextID: 1},
		CreateQueueSize:      2,
		CreateBatchSize:      1,
		CreateFlushInterval:  time.Millisecond,
		CreateEnqueueTimeout: time.Second,
	})

	_, err := svc.CreateShortUrl(context.Background(), "https://example.com", 0)
	if err == nil || !strings.Contains(err.Error(), "batch create short url") {
		t.Fatalf("CreateShortUrl() error = %v, want batch create context", err)
	}
}

func TestGetOriginUrlReturnsActiveStatus(t *testing.T) {
	expireAt := time.Unix(200, 0)
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			ExpireAt:  &expireAt,
		},
	}
	svc := NewShortUrlService(repo)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.GetOriginUrl(context.Background(), "000001", nil)
	if err != nil {
		t.Fatalf("GetOriginUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if got.Row.OriginURL != "https://example.com" {
		t.Fatalf("originURL = %q, want https://example.com", got.Row.OriginURL)
	}
}

func TestGetOriginUrlReturnsExpiredStatus(t *testing.T) {
	expireAt := time.Unix(100, 0)
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			ExpireAt:  &expireAt,
		},
	}
	svc := NewShortUrlService(repo)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.GetOriginUrl(context.Background(), "000001", nil)
	if err != nil {
		t.Fatalf("GetOriginUrl() unexpected error: %v", err)
	}
	if got.Status != StatusExpired {
		t.Fatalf("status = %q, want %q", got.Status, StatusExpired)
	}
}

func TestGetShortUrlReturnsExpiredStatus(t *testing.T) {
	expireAt := time.Unix(99, 0)
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			ExpireAt:  &expireAt,
		},
	}
	svc := NewShortUrlService(repo)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusExpired {
		t.Fatalf("status = %q, want %q", got.Status, StatusExpired)
	}
}

func TestDeleteShortUrl(t *testing.T) {
	repo := &mockShortUrlRepo{deleted: true}
	svc := NewShortUrlService(repo)

	deleted, err := svc.DeleteShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("DeleteShortUrl() unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("deleted = false, want true")
	}
	if repo.deleteShortCode != "000001" {
		t.Fatalf("deleteShortCode = %q, want 000001", repo.deleteShortCode)
	}
}

func TestDeleteShortUrlNotFound(t *testing.T) {
	repo := &mockShortUrlRepo{deleted: false}
	svc := NewShortUrlService(repo)

	deleted, err := svc.DeleteShortUrl(context.Background(), "missing")
	if err != nil {
		t.Fatalf("DeleteShortUrl() unexpected error: %v", err)
	}
	if deleted {
		t.Fatal("deleted = true, want false")
	}
}

func TestGetShortUrlCacheHitActiveSkipsRepo(t *testing.T) {
	repo := &mockShortUrlRepo{}
	cache := &mockShortUrlCache{
		getEntry: &cachepkg.ShortUrlEntry{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			CreatedAt: 50,
			ExpireAt:  200,
			Status:    StatusActive,
		},
	}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if got.Row == nil || got.Row.OriginURL != "https://example.com" {
		t.Fatalf("row = %#v, want cached row", got.Row)
	}
	if repo.findCalled {
		t.Fatal("repo should not be called on active cache hit")
	}
}

func TestGetShortUrlCacheHitNotFoundSkipsRepo(t *testing.T) {
	repo := &mockShortUrlRepo{}
	cache := &mockShortUrlCache{
		getEntry: &cachepkg.ShortUrlEntry{
			ShortCode: "000404",
			Status:    StatusNotFound,
		},
	}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)

	got, err := svc.GetShortUrl(context.Background(), "000404")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusNotFound {
		t.Fatalf("status = %q, want %q", got.Status, StatusNotFound)
	}
	if repo.findCalled {
		t.Fatal("repo should not be called on not_found cache hit")
	}
}

func TestGetShortUrlCacheMissWritesRedis(t *testing.T) {
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			CreatedAt: time.Unix(50, 0),
		},
	}
	cache := &mockShortUrlCache{getErr: cachepkg.ErrMiss}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if !repo.findCalled {
		t.Fatal("repo should be called on cache miss")
	}
	if !cache.setCalled {
		t.Fatal("cache should be written after mysql hit")
	}
	if cache.setEntry.ShortCode != "000001" || cache.setEntry.Status != StatusActive {
		t.Fatalf("setEntry = %#v, want active 000001", cache.setEntry)
	}
	if cache.setTTL != 24*time.Hour {
		t.Fatalf("setTTL = %s, want 24h", cache.setTTL)
	}
}

func TestGetShortUrlCacheMissWritesNotFound(t *testing.T) {
	repo := &mockShortUrlRepo{}
	cache := &mockShortUrlCache{getErr: cachepkg.ErrMiss}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)

	got, err := svc.GetShortUrl(context.Background(), "000404")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusNotFound {
		t.Fatalf("status = %q, want %q", got.Status, StatusNotFound)
	}
	if !cache.setCalled {
		t.Fatal("cache should be written after mysql not found")
	}
	if cache.setEntry.Status != StatusNotFound {
		t.Fatalf("setEntry.Status = %q, want %q", cache.setEntry.Status, StatusNotFound)
	}
	if cache.setTTL != time.Minute {
		t.Fatalf("setTTL = %s, want 1m", cache.setTTL)
	}
}

func TestGetShortUrlCacheErrorFallsBackToRepo(t *testing.T) {
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
		},
	}
	cache := &mockShortUrlCache{getErr: errors.New("redis down")}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if !repo.findCalled {
		t.Fatal("repo should be called when cache get fails")
	}
}

func TestGetShortUrlRejectsInvalidShortCodeBeforeRepo(t *testing.T) {
	repo := &mockShortUrlRepo{}
	remote := &mockShortUrlCache{}
	local := &mockShortUrlCache{}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: remote,
		LocalCache:  local,
	})

	got, err := svc.GetShortUrl(context.Background(), "bad")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusNotFound {
		t.Fatalf("status = %q, want %q", got.Status, StatusNotFound)
	}
	if repo.findCalled {
		t.Fatal("repo should not be called for invalid short code")
	}
	if remote.getCalled || remote.setCalled || local.getCalled || local.setCalled {
		t.Fatal("cache should not be read or written for invalid short code")
	}
}

func TestGetShortUrlBloomMissSkipsRepo(t *testing.T) {
	repo := &mockShortUrlRepo{}
	bloom := &mockBloomFilter{exists: false}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: cachepkg.NewNoopShortUrlCache(),
		LocalCache:  cachepkg.NewNoopShortUrlCache(),
		BloomFilter: bloom,
	})

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusNotFound {
		t.Fatalf("status = %q, want %q", got.Status, StatusNotFound)
	}
	if !bloom.existsCalled || bloom.existsCode != "000001" {
		t.Fatalf("bloom exists called=%v code=%q, want 000001", bloom.existsCalled, bloom.existsCode)
	}
	if repo.findCalled {
		t.Fatal("repo should not be called when bloom says short code is absent")
	}
}

func TestGetShortUrlBloomErrorFallsBackToRepo(t *testing.T) {
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
		},
	}
	bloom := &mockBloomFilter{existsErr: errors.New("redis down")}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: cachepkg.NewNoopShortUrlCache(),
		LocalCache:  cachepkg.NewNoopShortUrlCache(),
		BloomFilter: bloom,
	})

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if !repo.findCalled {
		t.Fatal("repo should be called when bloom check fails")
	}
}

func TestDeleteShortUrlDeletesCacheBeforeAndAfterRepo(t *testing.T) {
	repo := &mockShortUrlRepo{deleted: true}
	cache := &mockShortUrlCache{}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)

	deleted, err := svc.DeleteShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("DeleteShortUrl() unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("deleted = false, want true")
	}
	if cache.deleteCount != 2 {
		t.Fatalf("deleteCount = %d, want 2", cache.deleteCount)
	}
	if !repo.deleteCalled {
		t.Fatal("repo delete should be called")
	}
}

func TestDeleteShortUrlIgnoresCacheDeleteError(t *testing.T) {
	repo := &mockShortUrlRepo{deleted: true}
	cache := &mockShortUrlCache{delErr: errors.New("redis down")}
	svc := newShortUrlServiceWithRemoteCache(repo, cache, 24*time.Hour, time.Minute)

	deleted, err := svc.DeleteShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("DeleteShortUrl() unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("deleted = false, want true")
	}
}

func TestCacheTTLUsesExpireAtForActiveShortUrl(t *testing.T) {
	expireAt := time.Unix(200, 0)
	svc := newShortUrlServiceWithRemoteCache(&mockShortUrlRepo{}, &mockShortUrlCache{}, 24*time.Hour, time.Minute)
	svc.now = func() time.Time { return time.Unix(100, 0) }

	got := svc.cacheTTL(&shortlink.Link{ExpireAt: &expireAt}, StatusActive)
	if got != 100*time.Second {
		t.Fatalf("cacheTTL() = %s, want 100s", got)
	}
}

func TestGetShortUrlLocalCacheHitSkipsRemoteAndRepo(t *testing.T) {
	repo := &mockShortUrlRepo{}
	local := &mockShortUrlCache{
		getEntry: &cachepkg.ShortUrlEntry{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			CreatedAt: 50,
			Status:    StatusActive,
		},
	}
	remote := &mockShortUrlCache{getErr: cachepkg.ErrMiss}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: remote,
		LocalCache:  local,
	})

	got, err := svc.GetShortUrl(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrl() unexpected error: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status = %q, want %q", got.Status, StatusActive)
	}
	if remote.getCalled {
		t.Fatal("remote cache should not be called on local cache hit")
	}
	if repo.findCalled {
		t.Fatal("repo should not be called on local cache hit")
	}
}

func TestGetShortUrlSingleflightMergesRepoLookups(t *testing.T) {
	repo := &mockShortUrlRepo{
		findDelay: 50 * time.Millisecond,
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
			CreatedAt: time.Unix(50, 0),
		},
	}
	remote := &mockShortUrlCache{getErr: cachepkg.ErrMiss}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: remote,
		LocalCache:  cachepkg.NewNoopShortUrlCache(),
	})

	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := svc.GetShortUrl(context.Background(), "000001")
			if err != nil {
				errs <- err
				return
			}
			if got.Status != StatusActive {
				errs <- errors.New("unexpected status")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	repo.mu.Lock()
	findCount := repo.findCount
	repo.mu.Unlock()
	if findCount != 1 {
		t.Fatalf("findCount = %d, want 1", findCount)
	}
}

func TestJitterTTLDoesNotApplyToExpireAt(t *testing.T) {
	expireAt := time.Unix(200, 0)
	svc := NewShortUrlServiceWithOptions(&mockShortUrlRepo{}, ShortUrlServiceOptions{
		CacheJitterRatio: 0.1,
	})
	svc.now = func() time.Time { return time.Unix(100, 0) }
	svc.jitter = func(ttl time.Duration, ratio float64) time.Duration {
		return ttl + time.Second
	}

	got := svc.cacheTTL(&shortlink.Link{ExpireAt: &expireAt}, StatusActive)
	if got != 100*time.Second {
		t.Fatalf("cacheTTL() = %s, want 100s without jitter", got)
	}

	got = svc.cacheTTL(&shortlink.Link{}, StatusActive)
	if got != 24*time.Hour+time.Second {
		t.Fatalf("cacheTTL() = %s, want default ttl with jitter", got)
	}
}

func TestGetOriginUrlEnqueuesVisitOnlyForActive(t *testing.T) {
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
		},
	}
	visitRepo := &mockVisitRepo{}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache:      cachepkg.NewNoopShortUrlCache(),
		LocalCache:       cachepkg.NewNoopShortUrlCache(),
		VisitRepo:        visitRepo,
		VisitQueueSize:   1,
		VisitWorkerCount: 0,
		IPHashSalt:       "salt",
	})
	svc.now = func() time.Time { return time.Unix(100, 0) }

	_, err := svc.GetOriginUrl(context.Background(), "000001", &VisitInfo{
		ClientIP:  "192.0.2.1",
		UserAgent: "agent",
		Referer:   "referer",
	})
	if err != nil {
		t.Fatalf("GetOriginUrl() unexpected error: %v", err)
	}

	select {
	case visit := <-svc.visitCh:
		if visit.ShortCode != "000001" || visit.UserAgent != "agent" || visit.Referer != "referer" {
			t.Fatalf("visit = %#v, want metadata", visit)
		}
		if len(visit.IP) != 64 || visit.IP == "192.0.2.1" {
			t.Fatalf("hashed IP = %q, want 64-char hash", visit.IP)
		}
	default:
		t.Fatal("expected visit to be enqueued")
	}
}

func TestGetOriginUrlVisitQueueFullDoesNotBlock(t *testing.T) {
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
		},
	}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache:      cachepkg.NewNoopShortUrlCache(),
		LocalCache:       cachepkg.NewNoopShortUrlCache(),
		VisitRepo:        &mockVisitRepo{},
		VisitQueueSize:   1,
		VisitWorkerCount: 0,
	})
	svc.visitCh <- shortlink.Visit{ShortCode: "already-full"}

	_, err := svc.GetOriginUrl(context.Background(), "000001", &VisitInfo{ClientIP: "192.0.2.1"})
	if err != nil {
		t.Fatalf("GetOriginUrl() unexpected error: %v", err)
	}
}

func TestGetShortUrlStats(t *testing.T) {
	lastVisitedAt := time.Unix(123, 0)
	repo := &mockShortUrlRepo{
		findRow: &shortlink.Link{
			ShortCode: "000001",
			OriginURL: "https://example.com",
		},
	}
	visitRepo := &mockVisitRepo{
		stats: &shortlink.Stats{
			ShortCode:     "000001",
			PV:            10,
			UV:            3,
			LastVisitedAt: &lastVisitedAt,
			TopReferers:   []shortlink.StatsItem{{Value: "referer", Count: 4}},
			TopUserAgents: []shortlink.StatsItem{{Value: "agent", Count: 5}},
		},
	}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: cachepkg.NewNoopShortUrlCache(),
		LocalCache:  cachepkg.NewNoopShortUrlCache(),
		VisitRepo:   visitRepo,
	})

	got, err := svc.GetShortUrlStats(context.Background(), "000001")
	if err != nil {
		t.Fatalf("GetShortUrlStats() unexpected error: %v", err)
	}
	if got.Status != StatusActive || got.Stats.PV != 10 || got.Stats.UV != 3 {
		t.Fatalf("stats result = %#v", got)
	}
	if !visitRepo.statsCalled || visitRepo.statsCode != "000001" {
		t.Fatalf("stats repo called=%v code=%q, want true 000001", visitRepo.statsCalled, visitRepo.statsCode)
	}
}

func TestGetShortUrlStatsNotFound(t *testing.T) {
	repo := &mockShortUrlRepo{}
	visitRepo := &mockVisitRepo{}
	svc := NewShortUrlServiceWithOptions(repo, ShortUrlServiceOptions{
		RemoteCache: cachepkg.NewNoopShortUrlCache(),
		LocalCache:  cachepkg.NewNoopShortUrlCache(),
		VisitRepo:   visitRepo,
	})

	got, err := svc.GetShortUrlStats(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetShortUrlStats() unexpected error: %v", err)
	}
	if got.Status != StatusNotFound {
		t.Fatalf("status = %q, want %q", got.Status, StatusNotFound)
	}
	if visitRepo.statsCalled {
		t.Fatal("stats repo should not be called for missing short url")
	}
}

func TestHashIPStable(t *testing.T) {
	svc := NewShortUrlServiceWithOptions(&mockShortUrlRepo{}, ShortUrlServiceOptions{IPHashSalt: "salt"})

	first := svc.hashIP("192.0.2.1")
	second := svc.hashIP("192.0.2.1")
	if first != second {
		t.Fatalf("hashes differ: %q != %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
	if first == "192.0.2.1" {
		t.Fatal("hash should not store raw IP")
	}
}
