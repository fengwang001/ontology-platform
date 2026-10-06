package settlement

import "container/heap"

// dayHeap is a min-heap of transaction days for which a bucket ever existed.
type dayHeap []Day

func (h dayHeap) Len() int           { return len(h) }
func (h dayHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h dayHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dayHeap) Push(x any)        { *h = append(*h, x.(Day)) }
func (h *dayHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// dayAggregator accumulates unsettled transactions grouped by their raw
// (not necessarily business) transaction day.
//
// Incremental extraction: removeThrough(b) removes every bucket with day <= b
// except the single greatest such bucket g. g is the only transaction day on
// which later transactions can legally arrive (it is the last settled
// boundary day, relevant when N == 1); the watermark extracted[d] records how
// much of each day was already returned, so a day that was retained on one
// call and graduates on the next contributes only the delta and never its full
// value twice. Watermarks are retained permanently (O(number of distinct
// transaction days) memory, O(1) per transaction) — never rescanned.
//
// The queued set guarantees every live bucket appears at most once in the
// heap; a retained bucket is re-enqueued only when popped. Thus total heap
// work across the merchant's lifetime is linear in bucket insertions times
// log of the number of live buckets, and the per-settled-day cost depends only
// on buckets crossing that day's boundary, never on the full history.
type dayAggregator struct {
	buckets   map[Day]Amount
	extracted map[Day]Amount
	queued    map[Day]struct{}
	order     dayHeap
}

func newDayAggregator() *dayAggregator {
	return &dayAggregator{
		buckets:   make(map[Day]Amount),
		extracted: make(map[Day]Amount),
		queued:    make(map[Day]struct{}),
	}
}

func (a *dayAggregator) add(day Day, amt Amount) {
	if _, ok := a.buckets[day]; !ok {
		a.buckets[day] = 0
		if _, q := a.queued[day]; !q {
			a.queued[day] = struct{}{}
			heap.Push(&a.order, day)
		}
	}
	a.buckets[day] += amt
}

// removeThrough sums and removes every bucket with day <= boundary except the
// greatest such bucket, which is retained (with its extraction watermark
// advanced) so later same-day transactions contribute once on the next call.
func (a *dayAggregator) removeThrough(boundary Day) Amount {
	var total Amount
	var greatest Day
	haveGreatest := false

	due := make([]Day, 0)
	for len(a.order) > 0 && a.order[0] <= boundary {
		d := heap.Pop(&a.order).(Day)
		delete(a.queued, d)
		if _, ok := a.buckets[d]; !ok {
			continue
		}
		due = append(due, d)
		if !haveGreatest || d > greatest {
			greatest = d
			haveGreatest = true
		}
	}

	for _, d := range due {
		total += a.buckets[d] - a.extracted[d]
		if d != greatest {
			// Graduates past the boundary: drop the live bucket. The watermark
			// is intentionally kept, because the same map key may have been
			// retained at the previous boundary and must not be re-counted if
			// a stale entry ever surfaces.
			delete(a.buckets, d)
		} else {
			a.extracted[d] = a.buckets[d]
			// Re-enqueue exactly once: it was popped above.
			a.queued[d] = struct{}{}
			heap.Push(&a.order, d)
		}
	}
	return total
}

// liveBuckets reports the number of retained buckets (diagnostics/tests).
func (a *dayAggregator) liveBuckets() int { return len(a.buckets) }
