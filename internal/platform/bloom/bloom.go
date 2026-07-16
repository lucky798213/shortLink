package bloom

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"

	"github.com/redis/go-redis/v9"
)

type Filter interface {
	Add(ctx context.Context, value string) error
	AddMany(ctx context.Context, values []string) error
	Exists(ctx context.Context, value string) (bool, error)
}

type RedisFilter struct {
	client redis.Cmdable
	key    string
	bits   uint64
	hashes uint64
}

func NewRedisFilter(client redis.Cmdable, key string, bits uint64, hashes uint64) *RedisFilter {
	if key == "" {
		key = "short_url:bloom:active"
	}
	if bits == 0 {
		bits = 1 << 28
	}
	if hashes == 0 {
		hashes = 7
	}
	return &RedisFilter{
		client: client,
		key:    key,
		bits:   bits,
		hashes: hashes,
	}
}

func (f *RedisFilter) Add(ctx context.Context, value string) error {
	return f.AddMany(ctx, []string{value})
}

func (f *RedisFilter) AddMany(ctx context.Context, values []string) error {
	if len(values) == 0 {
		return nil
	}
	pipe := f.client.Pipeline()
	for _, value := range values {
		for _, offset := range f.offsets(value) {
			pipe.SetBit(ctx, f.key, int64(offset), 1)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis bloom add: %w", err)
	}
	return nil
}

func (f *RedisFilter) Exists(ctx context.Context, value string) (bool, error) {
	//判断这个key 存不存在
	keyExists, err := f.client.Exists(ctx, f.key).Result()
	if err != nil {
		return true, fmt.Errorf("redis bloom exists key: %w", err)
	}
	if keyExists == 0 {
		return true, nil
	}

	//pipe 负责将多条命令一块执行
	pipe := f.client.Pipeline()

	//创建一个数组，用来保存每个 GETBIT 命令的结果。（接收 Redis 返回整数 的命令结果）
	cmds := make([]*redis.IntCmd, 0, f.hashes)

	//根据短码算出多个 Redis bit 位置
	for _, offset := range f.offsets(value) {
		cmds = append(cmds, pipe.GetBit(ctx, f.key, int64(offset)))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return true, fmt.Errorf("redis bloom get bits: %w", err)
	}
	for _, cmd := range cmds {
		if cmd.Val() == 0 {
			return false, nil
		}
	}
	return true, nil
}

func (f *RedisFilter) offsets(value string) []uint64 {
	h1 := hashWithSeed(value, 0x9e3779b97f4a7c15)
	h2 := hashWithSeed(value, 0xc2b2ae3d27d4eb4f)
	if h2 == 0 {
		h2 = 1
	}
	offsets := make([]uint64, 0, f.hashes)
	for i := uint64(0); i < f.hashes; i++ {
		offsets = append(offsets, (h1+i*h2)%f.bits)
	}
	return offsets
}

func hashWithSeed(value string, seed uint64) uint64 {
	//创建的是 FNV-1a 64-bit 哈希算法。
	hasher := fnv.New64a()

	//准备一个 8 字节数组
	var buf [8]byte

	//把一个 无符号 64 位整数（seed），按照 小端序（LittleEndian） 的规则，写入到字节切片 buf 里。
	binary.LittleEndian.PutUint64(buf[:], seed)

	//把 seed 的字节写入哈希器
	_, _ = hasher.Write(buf[:])

	//把 value 的字节写入哈希器
	_, _ = hasher.Write([]byte(value))
	return hasher.Sum64()
}

type AllowAllFilter struct{}

func (AllowAllFilter) Add(context.Context, string) error {
	return nil
}

func (AllowAllFilter) AddMany(context.Context, []string) error {
	return nil
}

func (AllowAllFilter) Exists(context.Context, string) (bool, error) {
	return true, nil
}
