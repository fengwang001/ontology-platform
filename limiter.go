package ontology

import (
	"time"
)

// Limiter is a sharded, multi-tenant fixed-point token bucket limiter.
type Limiter struct {
	shardCount uint64
	shards     []*shard
}

type shard struct {
	mu      rwMutex
	buckets map[string]*bucket
}

// Config describes one tenant's independent token bucket.
type Config struct {
	Capacity int64
	Rate     int64 // Whole tokens added per second.
}

// NewLimiter creates a limiter with fixed per-tenant shard count.
// A shard count below one is replaced by a small useful default.
func NewLimiter(shardCount uint64) *Limiter {
	if shardCount == 0 {
		shardCount = 64
	}

	shards := make([]*shard, shardCount)
	for i := range shards {
		shards[i] = &shard{buckets: make(map[string]*bucket)}
	}
	return &Limiter{shardCount: shardCount, shards: shards}
}

// AddTenant registers a tenant and starts it with a full bucket.
func (l *Limiter) AddTenant(tenant string, config Config, now time.Time) error {
	if config.Capacity <= 0 || config.Rate <= 0 {
		return ErrInvalidConfig
	}

	item := newFullBucket(config.Capacity, config.Rate, now)
	target := l.shardFor(tenant)
	target.mu.lock()
	defer target.mu.unlock()
	if _, exists := target.buckets[tenant]; exists {
		return ErrTenantAlreadyExists
	}
	target.buckets[tenant] = item
	return nil
}

// ActiveTenants returns the number of currently retained buckets.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for _, target := range l.shards {
		target.mu.rLock()
		total += len(target.buckets)
		target.mu.rUnlock()
	}
	return total
}

func (l *Limiter) shardFor(tenant string) *shard {
	return l.shards[fnv1a(tenant)%l.shardCount]
}

func fnv1a(value string) uint64 {
	hash := uint64(14695981039346656037)
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	return hash
}
