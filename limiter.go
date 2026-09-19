package ontology

import (
	"hash/fnv"
	"time"
)

// Limiter is a concurrency-safe, multi-tenant fixed-rate token bucket limiter.
//
// Time is supplied by callers to every operation. If now is earlier than the
// tenant's previous timestamp, Allow rejects that call with ErrTimeRewound and
// leaves the bucket untouched. Equal timestamps make repeated calls behave as a
// single instant.
type Limiter struct {
	rate     int
	capacity int
	idleTTL  time.Duration
	mask     uint64
	shards   []*shard
}

// NewLimiter creates a limiter. Rate is tokens per second and capacity is the
// maximum number of whole tokens a bucket can hold.
func NewLimiter(cfg Config) (*Limiter, error) {
	cfg, valid := cfg.normalized()
	if !valid {
		return nil, ErrInvalidConfig
	}

	shards := make([]*shard, cfg.ShardCount)
	for index := range shards {
		shards[index] = newShard()
	}
	return &Limiter{
		rate:     cfg.Rate,
		capacity: cfg.Capacity,
		idleTTL:  cfg.IdleTTL,
		mask:     uint64(cfg.ShardCount - 1),
		shards:   shards,
	}, nil
}

// Allow attempts to remove n tokens for tenant at injected time now.
// n equal to zero always succeeds and consumes no tokens. A negative n returns
// ErrNegativeTokens; n greater than capacity returns ErrRequestExceedsCapacity.
// On a temporary shortage it returns *InsufficientTokensError without changing
// bucket state.
func (l *Limiter) Allow(tenant string, n int, now time.Time) error {
	if n < 0 {
		return ErrNegativeTokens
	}
	if n > l.capacity {
		return ErrRequestExceedsCapacity
	}
	if n == 0 {
		return nil
	}
	return l.shardFor(tenant).allow(tenant, n, l.capacity, l.rate, l.idleTTL, now)
}

// Available returns the whole tokens currently available for tenant. It applies
// any refill due at now, but never returns accumulated fractional tokens as an
// extra whole token.
func (l *Limiter) Available(tenant string, now time.Time) int {
	return l.shardFor(tenant).available(tenant, l.capacity, l.rate, l.idleTTL, now)
}

// ActiveTenants returns the number of retained tenant buckets. Buckets whose
// idle limit has elapsed remain counted until a related call or
// EvictInactive reclaims them.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for _, current := range l.shards {
		total += current.active()
	}
	return total
}

// EvictInactive removes buckets not observed since now-idleTTL. It takes each
// shard lock independently and does not touch buckets being accessed in other
// shards. A recreated tenant starts with a full bucket.
func (l *Limiter) EvictInactive(now time.Time) {
	for _, current := range l.shards {
		current.evictInactive(now, l.idleTTL)
	}
}

func (l *Limiter) shardFor(tenant string) *shard {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(tenant))
	return l.shards[hash.Sum64()&l.mask]
}
