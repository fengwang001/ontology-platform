// Package win holds the sliding-window core: an ascending FIFO of accepted
// timestamps with head-pointer eviction of expired entries.
package win

// Queue is a FIFO of accepted timestamps in non-decreasing order.
// Expired entries are dropped by advancing head (no whole-slice scan);
// probes records how many timestamps the latest EvictExpired inspected and
// must never leave the package through the public API.
type Queue struct {
	window int64
	ts     []int64
	head   int
	probes int
}

// New creates a queue for a window of the given positive length.
func New(window int64) *Queue { return &Queue{window: window} }

// Push appends a newly accepted timestamp. Callers guarantee t is not smaller
// than previously pushed timestamps.
func (q *Queue) Push(t int64) { q.ts = append(q.ts, t) }

// EvictExpired removes every timestamp strictly below t-window (the left edge
// is closed: a timestamp equal to t-window stays). It returns how many entries
// were evicted and stops inspecting as soon as the head is still fresh.
func (q *Queue) EvictExpired(t int64) int {
	cutoff := t - q.window
	q.probes = 0
	evicted := 0
	for q.head < len(q.ts) {
		q.probes++
		if q.ts[q.head] >= cutoff {
			break
		}
		q.head++
		evicted++
	}
	q.compact()
	return evicted
}

// InWindow reports the count of retained timestamps in [t-window, t].
// The right edge includes t: concurrent callers carrying the same timestamp
// compete for the same slots. Call after EvictExpired(t); with non-decreasing
// clocks every retained timestamp is at least t-window and at most t.
func (q *Queue) InWindow(t int64) int { return len(q.ts) - q.head }

// Snapshot returns the retained timestamps in ascending order.
func (q *Queue) Snapshot() []int64 {
	out := make([]int64, len(q.ts)-q.head)
	copy(out, q.ts[q.head:])
	return out
}

// compact reclaims prefix space once evicted entries dominate the slice.
func (q *Queue) compact() {
	switch {
	case q.head == 0:
		return
	case q.head == len(q.ts):
		q.ts = q.ts[:0]
		q.head = 0
	case q.head*2 >= len(q.ts):
		q.ts = append(q.ts[:0], q.ts[q.head:]...)
		q.head = 0
	}
}

// ProbeBoundVerified runs m-scale scenarios entirely inside the package and
// returns only a verdict: when nothing expires, EvictExpired must inspect at
// most a constant number of timestamps regardless of m. The probe count
// itself is never exposed.
func ProbeBoundVerified() bool {
	for _, m := range []int{100, 1000, 10000} {
		q := New(int64(m) + 1)
		for i := 0; i < m; i++ {
			q.Push(int64(i))
		}
		q.EvictExpired(int64(m - 1))
		if q.probes > 2 {
			return false
		}
	}
	return true
}
