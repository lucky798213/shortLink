package job

import (
	"context"
	"log/slog"
	"time"

	bloompkg "short_url/pkg/bloom"
	"short_url/rpc/repository"
	cachepkg "short_url/rpc/repository/cache"
)

type ExpiredCleanerOptions struct {
	Interval  time.Duration
	BatchSize int
	Now       func() time.Time
}

func StartExpiredCleaner(ctx context.Context, repo repository.ShortUrlMaintenanceRepo, cache cachepkg.ShortUrlCache, opts ExpiredCleanerOptions) {
	if repo == nil {
		return
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Minute
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 500
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	go func() {
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				codes, err := repo.SoftDeleteExpired(ctx, opts.Now(), opts.BatchSize)
				if err != nil {
					slog.Warn("clean expired short urls failed", "err", err)
					continue
				}
				for _, code := range codes {
					if err := cache.Delete(ctx, code); err != nil {
						slog.Warn("delete expired short url cache failed", "short_code", code, "err", err)
					}
				}
				if len(codes) > 0 {
					slog.Info("expired short urls cleaned", "count", len(codes))
				}
			}
		}
	}()
}

type BloomRebuilderOptions struct {
	Interval  time.Duration
	BatchSize int
}

func StartBloomRebuilder(ctx context.Context, repo repository.ShortUrlMaintenanceRepo, filter bloompkg.Filter, opts BloomRebuilderOptions) {
	if repo == nil || filter == nil {
		return
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Minute
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}

	rebuild := func() {
		count := 0
		err := repo.ScanActiveShortCodes(ctx, opts.BatchSize, func(codes []string) error {
			count += len(codes)
			return filter.AddMany(ctx, codes)
		})
		if err != nil {
			slog.Warn("rebuild short url bloom failed", "err", err)
			return
		}
		slog.Info("short url bloom rebuilt", "count", count)
	}

	go func() {
		rebuild()
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rebuild()
			}
		}
	}()
}
