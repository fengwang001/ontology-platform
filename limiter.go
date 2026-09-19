package ontology

import (
	"sync"
	"time"
)

const defaultShardCount = 64
const maxInt64 = int64(^uint64(0) >> 1)

type shard struct {
	mu      sync.RWMutex
	entries map[string]*bucket
}

// Limiter keeps independent token buckets for an unbounded set of tenants.
type Limiter struct {
	capacity int64
	rate     int64
	shards   []shard
}

func NewLimiter(capacity, rate int64) *Limiter {
	if capacity <= 0 || rate <= 0 || capacity > maxInt64/nanosPerSecond {
		panic("capacity and rate must be positive and capacity must fit fixed-point storage")
	}

	shards := make([]shard, defaultShardCount)
	for i := range shards {
		shards[i].entries = make(map[string]*bucket)
	}
	return &Limiter{capacity: capacity, rate: rate, shards: shards}
}

// Allow attempts to remove n tokens from tenant's bucket at the injected time.
// A time before that tenant's previous observed time returns ErrTimeReversed
// and leaves the bucket unchanged. An equal timestamp performs no refill and is
// therefore idempotent. Insufficient tokens return *InsufficientError with the
// earliest time at which the whole request can be accepted.
func (l *Limiter) Allow(tenant string, n int64, now time.Time) (bool, error) {
	if n < 0 {
		return false, ErrInvalidRequest
	}
	if n > l.capacity {
		return false, ErrRequestTooLarge
	}

	bucketValue, unlock := l.acquire(tenant, now)
	defer unlock()
	return bucketValue.allow(n, now, l.capacity, l.rate)
}

// Available returns the integer number of whole tokens currently available.
// Like Allow, it rejects a reversed time without mutating the bucket.
func (l *Limiter) Available(tenant string, now time.Time) (int64, error) {
	current := l.shardFor(tenant)
	current.mu.RLock()
	bucketValue := current.entries[tenant]
	if bucketValue == nil {
		current.mu.RUnlock()
		return l.capacity, nil
	}

	bucketValue.mu.Lock()
	defer bucketValue.mu.Unlock()
	if now.Before(bucketValue.lastTime) {
		available := bucketValue.available()
		current.mu.RUnlock()
		return available, ErrTimeReversed
	}
	bucketValue.refill(now, l.capacity, l.rate)
	available := bucketValue.available()
	current.mu.RUnlock()
	return available, nil
}

// ActiveTenants returns the number of tenant buckets currently retained.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for i := range l.shards {
		l.shards[i].mu.RLock()
		total += len(l.shards[i].entries)
		l.shards[i].mu.RUnlock()
	}
	return total
}

// EvictIdle removes buckets whose last successful or attempted observation was
// at or before now-idleFor. Buckets currently locked by another goroutine are
// protected by their shard's read lock and are not removed mid-operation.
func (l *Limiter) EvictIdle(idleFor time.Duration, now time.Time) int {
	if idleFor <= 0 {
		panic("idleFor must be positive")
	}

	removed := 0
	cutoff := now.Add(-idleFor)
	for i := range l.shards {
		current := &l.shards[i]
		current.mu.Lock()
		for tenant, bucketValue := range current.entries {
			bucketValue.mu.Lock()
			idle := !bucketValue.lastAccess.After(cutoff)
			bucketValue.mu.Unlock()
			if idle {
				delete(current.entries, tenant)
				removed++
			}
		}
		current.mu.Unlock()
	}
	return removed
}

func (l *Limiter) acquire(tenant string, now time.Time) (*bucket, func()) {
	current := l.shardFor(tenant)
	current.mu.RLock()
	bucketValue := current.entries[tenant]
	if bucketValue != nil {
		bucketValue.mu.Lock()
		return bucketValue, func() {
			bucketValue.mu.Unlock()
			current.mu.RUnlock()
		}
	}

	current.mu.RUnlock()
	current.mu.Lock()
	bucketValue = current.entries[tenant]
	if bucketValue == nil {
		bucketValue = newBucket(now, l.capacity)
		current.entries[tenant] = bucketValue
	}
	bucketValue.mu.Lock()
	current.mu.Unlock()
	return bucketValue, bucketValue.mu.Unlock
}

func (l *Limiter) shardFor(tenant string) *shard {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)

	hash := offset64
	for i := 0; i < len(tenant); i++ {
		hash ^= uint64(tenant[i])
		hash *= prime64
	}
	return &l.shards[hash%uint64(len(l.shards))]
}
