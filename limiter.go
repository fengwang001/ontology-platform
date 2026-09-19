package ontology

import (
	"hash/fnv"
	"sync"
	"time"
)

// Config configures a multi-tenant token-bucket Limiter. All tenants share
// the same capacity and refill rate; each tenant owns an independent
// bucket.
type Config struct {
	// Capacity is the maximum number of tokens a bucket can hold. Must be
	// positive.
	Capacity int64
	// Rate is the refill speed in whole tokens per second. Must be
	// positive.
	Rate int64
	// Shards is the number of independently locked tenant partitions.
	// Zero selects a built-in default. More shards mean less cross-tenant
	// lock contention at the cost of a little memory.
	Shards int
}

// Limiter is a concurrency-safe, sharded multi-tenant rate limiter.
//
// Tenants are created lazily, starting with a full bucket. There is no
// upper bound on the number of tenants other than memory; idle tenants can
// be reclaimed with ReclaimIdle. Time is always supplied by the caller:
// the limiter never reads the wall clock, which makes every result fully
// deterministic and testable.
type Limiter struct {
	capacity int64
	rate     int64
	shards   []*shard
}

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

const defaultShards = 64

// NewLimiter validates cfg and constructs an empty Limiter.
func NewLimiter(cfg Config) (*Limiter, error) {
	if cfg.Capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if cfg.Rate <= 0 {
		return nil, ErrInvalidRate
	}
	// Internal amounts are stored as capacity*scale and must fit int64.
	if cfg.Capacity > (1<<63-1)/scale {
		return nil, ErrInvalidCapacity
	}
	n := cfg.Shards
	if n <= 0 {
		n = defaultShards
	}
	l := &Limiter{
		capacity: cfg.Capacity,
		rate:     cfg.Rate,
		shards:   make([]*shard, n),
	}
	for i := range l.shards {
		l.shards[i] = &shard{buckets: make(map[string]*bucket)}
	}
	return l, nil
}

func (l *Limiter) shardFor(tenant string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(tenant))
	return l.shards[int(h.Sum32())%len(l.shards)]
}

// getBucket returns the tenant's bucket, creating a full one on first use.
// The caller must hold s.mu.
func (l *Limiter) getBucket(s *shard, tenant string) *bucket {
	b, ok := s.buckets[tenant]
	if !ok {
		b = newBucket(l.capacity, l.rate)
		s.buckets[tenant] = b
	}
	return b
}

// Allow reports whether n tokens may be taken from tenant's bucket at time
// now, consuming them on success.
//
// Error categories (use errors.Is):
//   - ErrInvalidRequest: n < 0 (parameter error, no state change).
//   - ErrRequestExceedsCapacity: n > capacity (can never succeed).
//   - ErrTimeReversed: now is earlier than a prior call (no state change).
//   - *InsufficientTokensError (matches ErrInsufficientTokens): valid
//     request, too few tokens; its RetryAfter gives the exact wait.
//
// n == 0 always succeeds and consumes nothing. A newly seen tenant starts
// with a full bucket.
func (l *Limiter) Allow(tenant string, n int64, now time.Time) (bool, error) {
	s := l.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	return l.getBucket(s, tenant).allow(n, now)
}

// Available returns the whole number of tokens tenant can spend at time now
// after applying all refill due up to now. The fractional remainder is
// kept internally. It returns ErrTimeReversed for a backwards timestamp
// without changing state. A missing tenant is reported as a full bucket
// without creating one.
func (l *Limiter) Available(tenant string, now time.Time) (int64, error) {
	s := l.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[tenant]
	if !ok {
		return l.capacity, nil
	}
	return b.available(now)
}

// ActiveTenants returns the number of currently tracked tenants.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for _, s := range l.shards {
		s.mu.Lock()
		total += len(s.buckets)
		s.mu.Unlock()
	}
	return total
}

// ReclaimIdle removes tenants whose most recent call was strictly before
// cutoff. Reclaimed tenants start over with a full bucket the next time
// they appear.
//
// Reclamation only locks one shard at a time and removes whole bucket
// entries; every in-flight Allow holds its shard lock (and thus keeps its
// bucket referenced) for the duration of the call, so a tenant can never
// be reclaimed while it is being accessed.
func (l *Limiter) ReclaimIdle(cutoff time.Time) int {
	reclaimed := 0
	for _, s := range l.shards {
		s.mu.Lock()
		for tenant, b := range s.buckets {
			if !b.last.IsZero() && b.last.Before(cutoff) {
				delete(s.buckets, tenant)
				reclaimed++
			}
		}
		s.mu.Unlock()
	}
	return reclaimed
}
