package sharding

import (
	"fmt"

	"short_url/pkg/generator"
)

const (
	DefaultShardCount = 64
	tablePrefix       = "short_urls_"
)

// Strategy routes short URL rows to physical tables.
type Strategy struct {
	shardCount uint64
}

func NewStrategy(shardCount int) Strategy {
	if shardCount <= 0 {
		shardCount = DefaultShardCount
	}
	return Strategy{shardCount: uint64(shardCount)}
}

func (s Strategy) ShardCount() int {
	return int(s.shardCount)
}

func (s Strategy) TableByID(id uint64) string {
	return fmt.Sprintf("%s%02d", tablePrefix, id%s.shardCount)
}

func (s Strategy) TableByShortCode(shortCode string) (string, uint64, error) {
	id, err := generator.Decode(shortCode)
	if err != nil {
		return "", 0, err
	}
	return s.TableByID(id), id, nil
}

func (s Strategy) Tables() []string {
	tables := make([]string, 0, s.shardCount)
	for i := uint64(0); i < s.shardCount; i++ {
		tables = append(tables, fmt.Sprintf("%s%02d", tablePrefix, i))
	}
	return tables
}
