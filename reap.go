package ontology

import "time"

// ReapInactive removes buckets whose last observed activity is before cutoff.
// Re-adding a reaped tenant starts again with a full bucket. The shard write
// lock waits for in-flight requests in that shard, so a reaped bucket cannot be
// removed while its Allow operation is still running.
func (l *Limiter) ReapInactive(cutoff time.Time) int {
	reaped := 0

	for _, target := range l.shards {
		target.mu.lock()
		for tenant, item := range target.buckets {
			item.mu.Lock()
			inactive := item.last.Before(cutoff) && !item.deleted
			if inactive {
				item.deleted = true
				delete(target.buckets, tenant)
				reaped++
			}
			item.mu.Unlock()
		}
		target.mu.unlock()
	}

	return reaped
}

// RemoveTenant immediately removes a tenant. Its next AddTenant starts full.
func (l *Limiter) RemoveTenant(tenant string) bool {
	target := l.shardFor(tenant)
	target.mu.lock()
	defer target.mu.unlock()

	item := target.buckets[tenant]
	if item == nil {
		return false
	}
	item.mu.Lock()
	item.deleted = true
	delete(target.buckets, tenant)
	item.mu.Unlock()
	return true
}
