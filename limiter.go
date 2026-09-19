package ontology

import "time"

// Limiter is a multi-tenant token-bucket rate limiter. Every tenant gets
// its own independent bucket with the same capacity and refill rate.
// Tenants are fully isolated: one tenant draining its bucket never affects
// any other tenant, and there is no upper bound on the number of tenants.
//
// Time is always supplied by the caller (the now argument); the limiter
// never reads a real clock, which makes behavior fully deterministic in
// tests. All methods are safe for concurrent use.
type Limiter struct {
	capacity      int64 // bucket size, in tokens
	capacityUnits int64 // bucket size, in fixed-point units
	rate          int64 // refill speed, in tokens per second
	idleTTL       time.Duration
	shards        [shardCount]shard
}

// NewLimiter returns a Limiter whose buckets hold up to capacity tokens
// and refill at rate tokens per second. Both must be in [0, maxTokens];
// NewLimiter panics otherwise. A rate of 0 means buckets never refill.
//
// idleTTL controls ReclaimIdle: tenants with no Allow call for longer
// than idleTTL become eligible for reclamation. idleTTL <= 0 disables
// reclamation entirely.
func NewLimiter(capacity, rate int64, idleTTL time.Duration) *Limiter {
	if capacity < 0 || capacity > maxTokens {
		panic("ontology: capacity out of range [0, maxTokens]")
	}
	if rate < 0 || rate > maxTokens {
		panic("ontology: rate out of range [0, maxTokens]")
	}
	l := &Limiter{
		capacity:      capacity,
		capacityUnits: capacity * unitsPerToken,
		rate:          rate,
		idleTTL:       idleTTL,
	}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	return l
}

// Allow reports whether n tokens can be consumed for tenant at now, and
// consumes them if so.
//
//   - n == 0 always succeeds and consumes nothing;
//   - n < 0 fails with ErrNegativeTokens (invalid argument);
//   - n > capacity fails immediately with a *CapacityError matching
//     ErrExceedsCapacity: it can never be satisfied, so no waiting helps;
//   - otherwise, if not enough tokens are available, it returns
//     (false, nil); call WaitDuration to learn how long to wait.
//
// A now earlier than the bucket's last update is treated as zero elapsed
// time (see the package documentation). Allow counts as tenant activity
// for idle reclamation.
func (l *Limiter) Allow(tenant string, n int64, now time.Time) (bool, error) {
	need, err := l.needUnits(tenant, n)
	if err != nil {
		return false, err
	}
	b := l.bucketFor(tenant, now)
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.After(b.lastAccess) {
		b.lastAccess = now
	}
	b.refill(l.capacityUnits, l.rate, now)
	if b.avail < need {
		return false, nil
	}
	b.avail -= need
	return true, nil
}

// WaitDuration reports how long after now the tenant must wait until n
// tokens are available, assuming no further consumption. It returns 0
// when n tokens are available right now, and -1 when the request can
// never be satisfied because the refill rate is 0. The errors are the
// same as Allow's. The result is exact to the nanosecond: waiting the
// returned duration and then calling Allow with the same n is guaranteed
// to succeed (absent other consumers), and one nanosecond less is not
// enough. It is a pure query: it creates no state and does not count as
// activity for idle reclamation.
func (l *Limiter) WaitDuration(tenant string, n int64, now time.Time) (time.Duration, error) {
	need, err := l.needUnits(tenant, n)
	if err != nil {
		return 0, err
	}
	avail := l.availableUnits(tenant, now)
	deficit := need - avail
	if deficit <= 0 {
		return 0, nil
	}
	if l.rate == 0 {
		return -1, nil
	}
	// Round up to whole nanoseconds; computed with division to avoid
	// any chance of overflow.
	wait := deficit / l.rate
	if deficit%l.rate != 0 {
		wait++
	}
	return time.Duration(wait), nil
}

// Available returns the tenant's available tokens at now as a fractional
// number. Unknown tenants are reported full, since a tenant always starts
// with a full bucket. It is a pure query: it creates no state and does
// not count as activity for idle reclamation.
func (l *Limiter) Available(tenant string, now time.Time) float64 {
	return float64(l.availableUnits(tenant, now)) / float64(unitsPerToken)
}

// ActiveTenants reports how many tenants currently have a live bucket.
func (l *Limiter) ActiveTenants() int {
	total := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.RLock()
		total += len(s.buckets)
		s.mu.RUnlock()
	}
	return total
}

// ReclaimIdle removes every tenant whose last Allow call is older than
// the configured idle TTL relative to now, and returns how many tenants
// were removed. A reclaimed tenant that reappears later starts over with
// a full bucket. Tenants with an Allow call in flight are never reclaimed
// out from under it: the in-flight call completes normally. ReclaimIdle
// is a no-op when the idle TTL is not positive.
func (l *Limiter) ReclaimIdle(now time.Time) int {
	if l.idleTTL <= 0 {
		return 0
	}
	removed := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		for tenant, b := range s.buckets {
			b.mu.Lock()
			if now.Sub(b.lastAccess) >= l.idleTTL {
				delete(s.buckets, tenant)
				removed++
			}
			b.mu.Unlock()
		}
		s.mu.Unlock()
	}
	return removed
}

// needUnits validates n and converts it to fixed-point units.
func (l *Limiter) needUnits(tenant string, n int64) (int64, error) {
	if n < 0 {
		return 0, ErrNegativeTokens
	}
	if n > l.capacity {
		return 0, &CapacityError{Tenant: tenant, Requested: n, Capacity: l.capacity}
	}
	return n * unitsPerToken, nil
}

// availableUnits returns the tenant's available units at now without
// mutating any state. Unknown tenants are reported full.
func (l *Limiter) availableUnits(tenant string, now time.Time) int64 {
	b := l.findBucket(tenant)
	if b == nil {
		return l.capacityUnits
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return refilledUnits(b.avail, l.capacityUnits, l.rate, b.last, now)
}

// findBucket returns the tenant's bucket, or nil if the tenant is unknown.
func (l *Limiter) findBucket(tenant string) *bucket {
	s := &l.shards[hashString(tenant)%shardCount]
	s.mu.RLock()
	b := s.buckets[tenant]
	s.mu.RUnlock()
	return b
}

// bucketFor returns the tenant's bucket, creating a full one if needed.
func (l *Limiter) bucketFor(tenant string, now time.Time) *bucket {
	s := &l.shards[hashString(tenant)%shardCount]
	s.mu.RLock()
	b := s.buckets[tenant]
	s.mu.RUnlock()
	if b != nil {
		return b
	}
	s.mu.Lock()
	if b = s.buckets[tenant]; b == nil {
		b = &bucket{avail: l.capacityUnits, last: now, lastAccess: now}
		s.buckets[tenant] = b
	}
	s.mu.Unlock()
	return b
}
