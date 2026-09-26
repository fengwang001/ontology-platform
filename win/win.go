// Package win holds the FIFO queue of accepted request timestamps.
// It depends on no other package in this module.
package win

// Queue is an ascending FIFO of accepted timestamps. Expired entries are
// dropped from the front via a head pointer; the backing slice is compacted
// when the discarded prefix grows large.
type Queue struct {
	ts   []int64
	head int

	// probes records how many historical timestamps the most recent
	// EvictExpired call examined. It is deliberately unexported: the
	// complexity proof must not leak through the public API.
	probes int
}

// Push appends a timestamp. Callers guarantee t is non-decreasing.
func (q *Queue) Push(t int64) {
	q.ts = append(q.ts, t)
}

// Len reports the number of retained (non-evicted) timestamps.
func (q *Queue) Len() int { return len(q.ts) - q.head }

// EvictExpired drops every retained timestamp strictly older than
// now-window, i.e. ts < now-window. Only the front is inspected: the loop
// stops the moment the oldest surviving entry is still in the window.
func (q *Queue) EvictExpired(now, window int64) {
	q.probes = 0
	cutoff := now - window
	for q.head < len(q.ts) {
		q.probes++
		if q.ts[q.head] >= cutoff {
			break // oldest entry is alive, and every later one is >= it
		}
		q.head++
	}
	q.compact()
}

// InWindow counts retained timestamps in the half-open interval
// [now-window, now). EvictExpired must have run first with the same now so
// that every retained entry is already >= now-window.
func (q *Queue) InWindow(now int64) int {
	i := q.head
	for i < len(q.ts) && q.ts[i] < now {
		i++
	}
	return i - q.head
}

// Snapshot returns a copy of the retained timestamps, ascending.
func (q *Queue) Snapshot() []int64 {
	out := make([]int64, q.Len())
	copy(out, q.ts[q.head:])
	return out
}

// CheckProbeBound verifies internally that eviction probes stay bounded by a
// small constant regardless of how many live entries the queue holds. The
// numeric probe count never crosses the package boundary.
func CheckProbeBound() error {
	for _, m := range []int{100, 1000, 10000} {
		q := &Queue{}
		for i := 0; i < m; i++ {
			q.Push(int64(i)) // dense, all still inside a huge window
		}
		q.EvictExpired(int64(m), int64(m)+1) // nothing expires; oldest is alive
		if q.Len() != m || q.probes > 1 {
			return errProbeBound{m: m, probes: q.probes}
		}
	}
	return nil
}

type errProbeBound struct {
	m, probes int
}

func (e errProbeBound) Error() string { return "win: eviction probes grew with m" }

func (q *Queue) compact() {
	if q.head == 0 {
		return
	}
	if q.head < len(q.ts) && q.head*2 < len(q.ts) {
		return
	}
	q.ts = append([]int64(nil), q.ts[q.head:]...)
	q.head = 0
}
