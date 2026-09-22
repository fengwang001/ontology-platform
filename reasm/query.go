package reasm

import (
	"time"

	"ontology/frag"
)

// Info is a read-only snapshot of one in-flight message.
type Info struct {
	Received  int           // distinct bytes covered so far
	Complete  bool          // whether [0, total) is fully covered
	Remaining time.Duration // time left before eviction
}

// Query reports the state of message id. Delivered or evicted messages
// report the zero Info.
func (r *Reassembler) Query(id string) Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	e, ok := r.msgs[id]
	if !ok {
		return Info{}
	}
	rem := e.deadline.Sub(r.now())
	if rem < 0 {
		rem = 0
	}
	return Info{
		Received:  e.set.Received(),
		Complete:  e.set.Complete(),
		Remaining: rem,
	}
}

// Intervals returns the normalized (sorted, disjoint, non-adjacent)
// received-interval list of message id, or nil if it is not in flight.
func (r *Reassembler) Intervals(id string) []frag.Interval {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	e, ok := r.msgs[id]
	if !ok {
		return nil
	}
	return e.set.Intervals()
}

// Used returns the bytes currently held by all in-flight messages.
func (r *Reassembler) Used() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked()
	return r.budget.Used()
}
