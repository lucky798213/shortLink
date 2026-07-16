package config

import (
	"errors"
	"fmt"
	"time"
)

type RPC struct {
	DB struct {
		DSN string `mapstructure:"dsn"`
	} `mapstructure:"db"`
	GRPC struct {
		Addr string `mapstructure:"addr"`
	} `mapstructure:"grpc"`
	Redis Redis `mapstructure:"redis"`
	Cache struct {
		DefaultTTL      time.Duration `mapstructure:"default_ttl"`
		NotFoundTTL     time.Duration `mapstructure:"not_found_ttl"`
		LocalTTL        time.Duration `mapstructure:"local_ttl"`
		LocalMaxEntries int           `mapstructure:"local_max_entries"`
		JitterRatio     float64       `mapstructure:"jitter_ratio"`
		LookupTimeout   time.Duration `mapstructure:"lookup_timeout"`
	} `mapstructure:"cache"`
	Stats struct {
		QueueSize     int           `mapstructure:"queue_size"`
		WorkerCount   int           `mapstructure:"worker_count"`
		BatchSize     int           `mapstructure:"batch_size"`
		FlushInterval time.Duration `mapstructure:"flush_interval"`
		IPHashSalt    string        `mapstructure:"ip_hash_salt"`
	} `mapstructure:"stats"`
	Sharding struct {
		Count int `mapstructure:"count"`
	} `mapstructure:"sharding"`
	IDAllocator struct {
		Step uint64 `mapstructure:"step"`
	} `mapstructure:"id_allocator"`
	WriteBuffer struct {
		QueueSize      int           `mapstructure:"queue_size"`
		BatchSize      int           `mapstructure:"batch_size"`
		FlushInterval  time.Duration `mapstructure:"flush_interval"`
		EnqueueTimeout time.Duration `mapstructure:"enqueue_timeout"`
	} `mapstructure:"write_buffer"`
	Bloom struct {
		Enabled          bool          `mapstructure:"enabled"`
		Key              string        `mapstructure:"key"`
		Bits             uint64        `mapstructure:"bits"`
		Hashes           uint64        `mapstructure:"hashes"`
		RebuildInterval  time.Duration `mapstructure:"rebuild_interval"`
		RebuildBatchSize int           `mapstructure:"rebuild_batch_size"`
	} `mapstructure:"bloom"`
	Cleanup struct {
		Interval  time.Duration `mapstructure:"interval"`
		BatchSize int           `mapstructure:"batch_size"`
	} `mapstructure:"cleanup"`
	Etcd    Etcd `mapstructure:"etcd"`
	Service struct {
		Name string `mapstructure:"name"`
		Addr string `mapstructure:"addr"`
	} `mapstructure:"service"`
	Logger Logger `mapstructure:"logger"`
}

func LoadRPC(configFile string) (RPC, error) {
	v, err := newViper("rpc", configFile)
	if err != nil {
		return RPC{}, err
	}
	setRPCDefaults(v)
	var cfg RPC
	if err := v.Unmarshal(&cfg); err != nil {
		return RPC{}, fmt.Errorf("解析 RPC 配置: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return RPC{}, err
	}
	return cfg, nil
}

func (c RPC) Validate() error {
	if c.DB.DSN == "" {
		return errors.New("RPC 配置 db.dsn 不能为空")
	}
	if c.GRPC.Addr == "" {
		return errors.New("RPC 配置 grpc.addr 不能为空")
	}
	if c.Sharding.Count <= 0 {
		return errors.New("RPC 配置 sharding.count 必须大于 0")
	}
	return nil
}

func setRPCDefaults(v interface{ SetDefault(string, any) }) {
	v.SetDefault("grpc.addr", ":50051")
	v.SetDefault("redis.addr", "127.0.0.1:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("cache.default_ttl", "24h")
	v.SetDefault("cache.not_found_ttl", "1m")
	v.SetDefault("cache.local_ttl", "5m")
	v.SetDefault("cache.local_max_entries", 10000)
	v.SetDefault("cache.jitter_ratio", 0.1)
	v.SetDefault("cache.lookup_timeout", "3s")
	v.SetDefault("stats.queue_size", 10000)
	v.SetDefault("stats.worker_count", 2)
	v.SetDefault("stats.batch_size", 100)
	v.SetDefault("stats.flush_interval", "1s")
	v.SetDefault("stats.ip_hash_salt", "short_url_dev_salt")
	v.SetDefault("sharding.count", 64)
	v.SetDefault("id_allocator.step", 1000)
	v.SetDefault("write_buffer.queue_size", 10000)
	v.SetDefault("write_buffer.batch_size", 128)
	v.SetDefault("write_buffer.flush_interval", "10ms")
	v.SetDefault("write_buffer.enqueue_timeout", "200ms")
	v.SetDefault("bloom.enabled", true)
	v.SetDefault("bloom.key", "short_url:bloom:active")
	v.SetDefault("bloom.bits", uint64(1<<28))
	v.SetDefault("bloom.hashes", 7)
	v.SetDefault("bloom.rebuild_interval", "10m")
	v.SetDefault("bloom.rebuild_batch_size", 1000)
	v.SetDefault("cleanup.interval", "1m")
	v.SetDefault("cleanup.batch_size", 500)
	v.SetDefault("etcd.enabled", false)
	v.SetDefault("etcd.endpoints", []string{"127.0.0.1:2379"})
	v.SetDefault("etcd.dial_timeout", "3s")
	v.SetDefault("etcd.lease_ttl", 10)
	v.SetDefault("service.name", "short-url-rpc")
	v.SetDefault("service.addr", "127.0.0.1:50051")
	v.SetDefault("logger.level", "info")
	v.SetDefault("logger.development", false)
}
