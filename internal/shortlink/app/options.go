package app

import "time"

type CacheOptions struct {
	DefaultTTL    time.Duration
	NotFoundTTL   time.Duration
	LocalTTL      time.Duration
	JitterRatio   float64
	LookupTimeout time.Duration
}

type CreateOptions struct {
	Buffered       bool
	QueueSize      int
	BatchSize      int
	FlushInterval  time.Duration
	EnqueueTimeout time.Duration
}

type VisitOptions struct {
	QueueSize     int
	WorkerCount   int
	BatchSize     int
	FlushInterval time.Duration
	IPHashSalt    string
}

type Options struct {
	RemoteCache Cache
	LocalCache  Cache
	Cache       CacheOptions

	IDAllocator IDAllocator
	Create      CreateOptions
	BloomFilter BloomFilter

	VisitStore VisitStore
	Visit      VisitOptions
}
