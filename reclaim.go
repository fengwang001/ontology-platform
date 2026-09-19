package ontology

import "time"

// ReclaimIdle removes tenant buckets that have not been touched for at
// least maxIdle relative to now, and returns how many were removed. A
// reclaimed tenant that reappears starts over with a full bucket.
//
// Reclaiming takes each shard lock briefly, so a tenant being accessed
// concurrently is never corrupted: a racing Allow either finishes before
// the reclaim or creates a fresh full bucket afterwards.
func (l *Limiter) ReclaimIdle(now time.Time, maxIdle time.Duration) int {
	removed := 0
	for i := range l.shards {
		s := &l.shards[i]
		s.mu.Lock()
		for tenant, b := range s.buckets {
			if now.Sub(b.last) >= maxIdle {
				delete(s.buckets, tenant)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}
