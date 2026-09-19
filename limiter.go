package ontology

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// Argument errors. Both are distinct from a simple "not enough tokens"
// rejection, which is reported through Decision instead of an error.
var (
	// ErrNegativeN is returned when n is negative.
	ErrNegativeN = errors.New("ratelimit: n must be >= 0")
	// ErrExceedsCapacity is returned when n is larger than the bucket
	// capacity and can therefore never be satisfied.
	ErrExceedsCapacity = errors.New("ratelimit: n exceeds bucket capacity and can never be satisfied")
)

// Decision is the outcome of an Allow call.
type Decision struct {
	// Allowed reports whether the n tokens were consumed.
	Allowed bool
	// Wait is how long until the request could be satisfied, zero when
	// Allowed is true. It is rounded up to the nanosecond. It is
	// math.MaxInt64 nanoseconds when the rate is zero and the deficit
	// can never refill.
	Wait time.Duration
}

// shardCount is the number of independent bucket shards. Tenants hash to
// shards, so different tenants almost never share a lock.
const shardCount = 64

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// Limiter is a multi-tenant token bucket rate limiter. It is safe for
// concurrent use. All time comes from the caller; the limiter never reads
// a real clock.
type Limiter struct {
	capacity  int64
	rate      int64
	capMicro  int64
	rateMicro int64
	shards    [shardCount]shard
}

// NewLimiter returns a Limiter where every tenant bucket holds up to
// capacity tokens and refills at ratePerSec tokens per second. New and
// reclaimed tenants start with a full bucket.
func NewLimiter(capacity, ratePerSec int64) (*Limiter, error) {
	if capacity <= 0 {
		return nil, fmt.Errorf("ratelimit: capacity must be > 0, got %d", capacity)
	}
	if ratePerSec < 0 {
		return nil, fmt.Errorf("ratelimit: rate must be >= 0, got %d", ratePerSec)
	}
	if capacity > maxTokens || ratePerSec > maxTokens {
		return nil, fmt.Errorf("ratelimit: capacity and rate must be <= %d", maxTokens)
	}
	l := &Limiter{
		capacity:  capacity,
		rate:      ratePerSec,
		capMicro:  capacity * micro,
		rateMicro: ratePerSec * micro,
	}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	return l, nil
}

func (l *Limiter) shardFor(tenant string) *shard {
	h := fnv.New64a()
	h.Write([]byte(tenant))
	return &l.shards[h.Sum64()%shardCount]
}

// bucketFor returns the tenant's bucket, creating a full one on first
// sight. The shard lock must be held.
func (s *shard) bucketFor(tenant string, now time.Time, capMicro int64) *bucket {
	b, ok := s.buckets[tenant]
	if !ok {
		b = newBucket(now, capMicro)
		s.buckets[tenant] = b
	}
	return b
}

// Allow reports whether the tenant may consume n tokens at time now,
// consuming them if so. See the package documentation for the exact time
// and error semantics.
func (l *Limiter) Allow(tenant string, n int64, now time.Time) (Decision, error) {
	if n < 0 {
		return Decision{}, ErrNegativeN
	}
	if n > l.capacity {
		return Decision{}, ErrExceedsCapacity
	}
	s := l.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bucketFor(tenant, now, l.capMicro)
	b.refill(now, l.rateMicro, l.capMicro)
	need := n * micro
	if b.avail >= need {
		b.avail -= need
		return Decision{Allowed: true}, nil
	}
	return Decision{Wait: waitFor(need-b.avail, l.rateMicro)}, nil
}

// Available returns the tenant's currently available whole tokens at time
// now. A tenant never seen before reports a full bucket.
func (l *Limiter) Available(tenant string, now time.Time) int64 {
	return l.availableMicro(tenant, now) / micro
}

// availableMicro is Available in micro-tokens, for precise tests.
func (l *Limiter) availableMicro(tenant string, now time.Time) int64 {
	s := l.shardFor(tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.buckets[tenant]
	if !ok {
		return l.capMicro
	}
	b.refill(now, l.rateMicro, l.capMicro)
	return b.avail
}

// ActiveTenants returns the number of tenants currently tracked.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		total += len(s.buckets)
		s.mu.Unlock()
	}
	return total
}
