package ontology

import (
	"sync"
	"time"
)

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newShard() *shard {
	return &shard{buckets: make(map[string]*bucket)}
}

func (s *shard) allow(tenant string, n, capacity, rate int, idleTTL time.Duration, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.buckets[tenant]
	if current != nil && now.Before(current.last) {
		return ErrTimeRewound
	}
	if current != nil && isIdle(current.last, now, idleTTL) {
		current = nil
		delete(s.buckets, tenant)
	}
	if current == nil {
		current = newBucket(capacity, rate, now)
		s.buckets[tenant] = current
	}

	current.refill(now)
	requested := int64(n) * tokenScale
	if current.available < requested {
		return &InsufficientTokensError{
			Available:  current.tokens(),
			Requested:  n,
			RetryAfter: current.waitFor(n, now),
		}
	}

	current.available -= requested
	return nil
}

func (s *shard) available(tenant string, capacity, rate int, idleTTL time.Duration, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.buckets[tenant]
	if current == nil {
		current = newBucket(capacity, rate, now)
		s.buckets[tenant] = current
		return current.tokens()
	}
	if now.Before(current.last) {
		return current.tokens()
	}
	if isIdle(current.last, now, idleTTL) {
		delete(s.buckets, tenant)
		current = newBucket(capacity, rate, now)
		s.buckets[tenant] = current
		return current.tokens()
	}
	current.refill(now)
	return current.tokens()
}

func (s *shard) active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buckets)
}

func (s *shard) evictInactive(now time.Time, idleTTL time.Duration) {
	if idleTTL <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for tenant, current := range s.buckets {
		if isIdle(current.last, now, idleTTL) {
			delete(s.buckets, tenant)
		}
	}
}

func isIdle(last, now time.Time, idleTTL time.Duration) bool {
	return idleTTL > 0 && now.Sub(last) > idleTTL
}
