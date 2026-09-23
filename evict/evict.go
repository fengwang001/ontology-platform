// Package evict decides which idle resources to reclaim. Idle entries
// are ordered by non-decreasing return time, so the expired ones form
// a prefix: the scan stops at the first non-expired entry (or at the
// MinIdle floor) and never walks the whole list.
package evict

import "time"

// Policy selects idle resources for eviction. checked counts how many
// entries the most recent Select inspected (<= evicted + 1).
type Policy struct {
	MaxIdle time.Duration
	MinIdle int
	checked int
}

// Select returns how many leading (oldest) entries of ats to evict.
// ats must be non-decreasing return times; an entry is expired iff its
// idle age is >= MaxIdle (exactly MaxIdle counts, 1ns less does not),
// and eviction never leaves fewer than MinIdle entries.
func (p *Policy) Select(ats []time.Time, now time.Time) int {
	p.checked = 0
	n := 0
	for n < len(ats) && len(ats)-n > p.MinIdle {
		p.checked++
		if now.Sub(ats[n]) < p.MaxIdle {
			break
		}
		n++
	}
	return n
}
