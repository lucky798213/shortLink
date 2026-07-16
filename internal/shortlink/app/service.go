package app

import (
	"context"
	"errors"
	"fmt"

	"short_url/internal/shortlink"
)

// Service 是给传输层使用的薄 Facade，具体职责分别委托给独立用例。
type Service struct {
	creator     *Creator
	resolver    *Resolver
	manager     *Manager
	stats       *StatsService
	visitWriter *VisitWriter
}

func NewService(store BatchLinkStore, opts Options) *Service {
	resolver := newResolver(store, opts.RemoteCache, opts.LocalCache, opts.BloomFilter, opts.Cache)
	return &Service{
		creator:     newCreator(store, opts.IDAllocator, opts.BloomFilter, opts.Create),
		resolver:    resolver,
		manager:     newManager(store, resolver),
		stats:       newStatsService(resolver, opts.VisitStore),
		visitWriter: newVisitWriter(opts.VisitStore, opts.Visit),
	}
}

func (s *Service) CreateShortUrl(ctx context.Context, originURL string, expireAtUnix int64) (string, error) {
	return s.creator.Create(ctx, originURL, expireAtUnix)
}

func (s *Service) GetOriginUrl(ctx context.Context, shortCode string, visitInfo *shortlink.VisitInfo) (*LinkResult, error) {
	result, err := s.resolver.Resolve(ctx, shortCode)
	if err != nil {
		return nil, err
	}
	if result != nil && result.Status == shortlink.StatusActive && result.Link != nil {
		s.visitWriter.Enqueue(result.Link.ShortCode, visitInfo)
	}
	return result, nil
}

func (s *Service) GetShortUrl(ctx context.Context, shortCode string) (*LinkResult, error) {
	return s.resolver.Resolve(ctx, shortCode)
}

func (s *Service) DeleteShortUrl(ctx context.Context, shortCode string) (bool, error) {
	return s.manager.Delete(ctx, shortCode)
}

func (s *Service) GetShortUrlStats(ctx context.Context, shortCode string) (*StatsResult, error) {
	return s.stats.Get(ctx, shortCode)
}

func (s *Service) Shutdown(ctx context.Context) error {
	var shutdownErrs []error
	if err := s.creator.Shutdown(ctx); err != nil {
		shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown creator: %w", err))
	}
	if err := s.visitWriter.Shutdown(ctx); err != nil {
		shutdownErrs = append(shutdownErrs, err)
	}
	return errors.Join(shutdownErrs...)
}
