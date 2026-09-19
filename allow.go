package ontology

import "time"

// Allow decides whether n whole tokens may be taken at the injected now.
// A successful decision atomically refills and consumes tokens. A shortage does
// not consume tokens; its error can be inspected with AsDenied.
func (l *Limiter) Allow(tenant string, n int64, now time.Time) (bool, error) {
	if n < 0 {
		return false, ErrNegativeTokens
	}
	if n == 0 {
		return true, nil
	}

	target := l.shardFor(tenant)
	target.mu.rLock()
	defer target.mu.rUnlock()

	item := target.buckets[tenant]
	if item == nil {
		return false, ErrUnknownTenant
	}
	if n > item.capacity/nanosPerSecond {
		return false, ErrRequestExceedsCapacity
	}

	item.mu.Lock()
	defer item.mu.Unlock()
	if item.deleted {
		return false, ErrUnknownTenant
	}
	if !item.refill(now) {
		return false, ErrClockMovedBack
	}

	requested := n * nanosPerSecond
	if item.available < requested {
		return false, &DeniedError{
			Requested: n,
			Available: item.availableTokens(),
			Wait:      waitFor(requested-item.available, item.rate),
		}
	}

	item.available -= requested
	return true, nil
}

// AvailableTokens refills through now without consuming and returns the floor
// of currently usable whole tokens.
func (l *Limiter) AvailableTokens(tenant string, now time.Time) (int64, error) {
	target := l.shardFor(tenant)
	target.mu.rLock()
	defer target.mu.rUnlock()

	item := target.buckets[tenant]
	if item == nil {
		return 0, ErrUnknownTenant
	}

	item.mu.Lock()
	defer item.mu.Unlock()
	if item.deleted {
		return 0, ErrUnknownTenant
	}
	if !item.refill(now) {
		return 0, ErrClockMovedBack
	}
	return item.availableTokens(), nil
}
