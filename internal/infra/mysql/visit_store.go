package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/gorm"

	"short_url/internal/shortlink"
	"short_url/internal/shortlink/app"
)

type shortUrlVisitRepo struct {
	db *gorm.DB
}

func NewShortUrlVisitRepo(db *gorm.DB) app.VisitStore {
	return &shortUrlVisitRepo{db: db}
}

var _ app.VisitStore = (*shortUrlVisitRepo)(nil)
var _ app.BatchVisitStore = (*shortUrlVisitRepo)(nil)

func (r *shortUrlVisitRepo) CreateVisit(ctx context.Context, visit shortlink.Visit) error {
	if visit.CreatedAt.IsZero() {
		visit.CreatedAt = time.Now()
	}
	row := struct {
		ShortCode string
		IP        string
		UserAgent string
		Referer   string
		CreatedAt time.Time
	}{
		ShortCode: visit.ShortCode,
		IP:        visit.IP,
		UserAgent: visit.UserAgent,
		Referer:   visit.Referer,
		CreatedAt: visit.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Table("short_url_visits").Create(&row).Error; err != nil {
		return fmt.Errorf("insert short_url_visit: %w", err)
	}
	return nil
}

func (r *shortUrlVisitRepo) BatchCreateVisits(ctx context.Context, visits []shortlink.Visit) error {
	if len(visits) == 0 {
		return nil
	}
	now := time.Now()
	rows := make([]struct {
		ShortCode string
		IP        string
		UserAgent string
		Referer   string
		CreatedAt time.Time
	}, 0, len(visits))
	for _, visit := range visits {
		if visit.CreatedAt.IsZero() {
			visit.CreatedAt = now
		}
		rows = append(rows, struct {
			ShortCode string
			IP        string
			UserAgent string
			Referer   string
			CreatedAt time.Time
		}{
			ShortCode: visit.ShortCode,
			IP:        visit.IP,
			UserAgent: visit.UserAgent,
			Referer:   visit.Referer,
			CreatedAt: visit.CreatedAt,
		})
	}
	if err := r.db.WithContext(ctx).Table("short_url_visits").Create(&rows).Error; err != nil {
		return fmt.Errorf("batch insert short_url_visits: %w", err)
	}
	return nil
}

func (r *shortUrlVisitRepo) GetStats(ctx context.Context, shortCode string, topN int) (*shortlink.Stats, error) {
	if topN <= 0 {
		topN = 5
	}

	stats := &shortlink.Stats{ShortCode: shortCode}
	if err := r.db.WithContext(ctx).
		Table("short_url_visits").
		Where("short_code = ?", shortCode).
		Count(&stats.PV).Error; err != nil {
		return nil, fmt.Errorf("count visits: %w", err)
	}

	if err := r.db.WithContext(ctx).
		Table("short_url_visits").
		Select("COUNT(DISTINCT ip)").
		Where("short_code = ?", shortCode).
		Scan(&stats.UV).Error; err != nil {
		return nil, fmt.Errorf("count unique visitors: %w", err)
	}

	var lastVisitedAt sql.NullTime
	if err := r.db.WithContext(ctx).
		Table("short_url_visits").
		Select("MAX(created_at)").
		Where("short_code = ?", shortCode).
		Scan(&lastVisitedAt).Error; err != nil {
		return nil, fmt.Errorf("get last visited at: %w", err)
	}
	if lastVisitedAt.Valid {
		stats.LastVisitedAt = &lastVisitedAt.Time
	}

	var topReferers []shortlink.StatsItem
	if err := r.db.WithContext(ctx).
		Table("short_url_visits").
		Select("referer AS value, COUNT(*) AS count").
		Where("short_code = ?", shortCode).
		Group("referer").
		Order("count DESC").
		Limit(topN).
		Scan(&topReferers).Error; err != nil {
		return nil, fmt.Errorf("get top referers: %w", err)
	}
	stats.TopReferers = topReferers

	var topUserAgents []shortlink.StatsItem
	if err := r.db.WithContext(ctx).
		Table("short_url_visits").
		Select("user_agent AS value, COUNT(*) AS count").
		Where("short_code = ?", shortCode).
		Group("user_agent").
		Order("count DESC").
		Limit(topN).
		Scan(&topUserAgents).Error; err != nil {
		return nil, fmt.Errorf("get top user agents: %w", err)
	}
	stats.TopUserAgents = topUserAgents

	return stats, nil
}
