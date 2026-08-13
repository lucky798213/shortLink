package mysqlstore

import (
	"context"
	"fmt"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"short_url/internal/shortlink/code"
)

const defaultAllocName = "short_url"

type MySQLIDAllocator struct {
	db       *gorm.DB
	name     string
	step     uint64 // 每次向数据库申请多少个 ID
	mu       sync.Mutex
	nextID   uint64
	rangeEnd uint64
	maxID    uint64
}

func NewMySQLIDAllocator(db *gorm.DB, step uint64) *MySQLIDAllocator {
	if step == 0 {
		step = 1000
	}
	return &MySQLIDAllocator{
		db:    db,
		name:  defaultAllocName,
		step:  step,
		maxID: code.MaxID,
	}
}

func (a *MySQLIDAllocator) NextID(ctx context.Context) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	//先看内存里有没有可用 ID
	//有就直接返回
	if a.nextID > 0 && a.nextID <= a.rangeEnd {
		id := a.nextID
		a.nextID++
		return id, nil
	}

	//如果内存号段用完，就调用 NextRange()
	start, end, err := a.NextRange(ctx, int(a.step))
	if err != nil {
		return 0, err
	}
	a.nextID = start + 1
	a.rangeEnd = end
	return start, nil
}

func (a *MySQLIDAllocator) NextRange(ctx context.Context, size int) (uint64, uint64, error) {
	//size 表示：这次要申请多少个 ID。
	if size <= 0 {
		size = int(a.step)
	}

	//两个变量用来保存这次申请到的 ID 范围
	var start, end uint64

	//开启一个事务，并把事务对象 tx 传给你
	//保证一连串操作要么全部成功，要么全部失败，不会出现 “一半成功一半失败” 的脏数据。
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		//SQL 的作用是：
		//如果 short_url_id_alloc 表里还没有当前 name 对应的记录，就插入一条。
		if err := tx.Exec("INSERT IGNORE INTO short_url_id_alloc (name, next_id) VALUES (?, ?)", a.name, uint64(1)).Error; err != nil {
			return fmt.Errorf("ensure id allocator row: %w", err)
		}

		//定义一个临时结构体 接收查询结果
		var row struct {
			Name   string
			NextID uint64
		}

		//加锁读取当前分配器记录
		if err := tx.Table("short_url_id_alloc").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("name = ?", a.name).
			First(&row).Error; err != nil {
			return fmt.Errorf("lock id allocator row: %w", err)
		}

		//把数据库里的 next_id 作为这次分配的起点。
		start = row.NextID
		if start == 0 {
			start = 1
		}

		//计算本次号段终点
		end = start + uint64(size) - 1

		//检查是否超过最大容量
		//防止 uint64 溢出
		//uint64 超过最大值后会回绕。
		if end > a.maxID || end < start {
			return fmt.Errorf("short code capacity exhausted")
		}

		//更新数据库中的 next_id
		if err := tx.Table("short_url_id_alloc").
			Where("name = ?", a.name).
			Update("next_id", end+1).Error; err != nil {
			return fmt.Errorf("advance id allocator: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}
