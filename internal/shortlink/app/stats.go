package app

import (
	"context"
	"fmt"

	"short_url/internal/shortlink"
)

type StatsService struct {
	resolver *Resolver
	store    VisitStore
}

func newStatsService(resolver *Resolver, store VisitStore) *StatsService {
	return &StatsService{resolver: resolver, store: store}
}

func (s *StatsService) Get(ctx context.Context, shortCode string) (*StatsResult, error) {
	result, err := s.resolver.Resolve(ctx, shortCode)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Status == shortlink.StatusNotFound || result.Link == nil {
		return &StatsResult{Status: shortlink.StatusNotFound}, nil
	}
	if s.store == nil {
		return &StatsResult{
			Status: result.Status,
			Stats:  &shortlink.Stats{ShortCode: shortCode},
		}, nil
	}
	stats, err := s.store.GetStats(ctx, shortCode, 5)
	if err != nil {
		return nil, fmt.Errorf("get short url stats: %w", err)
	}
	if stats == nil {
		stats = &shortlink.Stats{ShortCode: shortCode}
	}
	stats.ShortCode = shortCode
	return &StatsResult{Stats: stats, Status: result.Status}, nil
}
