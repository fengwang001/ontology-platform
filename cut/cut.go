// Package cut computes the aligned snapshot cut over per-partition
// delivered-event counts: C = min(counts), pending[p] = counts[p] - C,
// and decides backpressure rejections. It depends on nothing.
package cut

// Tracker tracks per-partition event counts and the aligned cut C.
// It maintains C incrementally via a frequency table, so computing C
// after a feed reads O(1) partition counters, never a full min scan.
// Not safe for concurrent use; callers must synchronize.
type Tracker struct {
	counts     []int
	freq       map[int]int // freq[v] = number of partitions with count v
	min        int
	maxPending int
}

// New returns a Tracker for numPartitions partitions. maxPending is the
// per-partition pending-buffer limit; a feed that would push a partition's
// pending above it is rejected.
func New(numPartitions, maxPending int) *Tracker {
	t := &Tracker{
		counts:     make([]int, numPartitions),
		freq:       map[int]int{0: numPartitions},
		maxPending: maxPending,
	}
	return t
}

// Cut returns the current alignment point C = min(counts).
func (t *Tracker) Cut() int { return t.min }

// Count returns how many events partition p has delivered.
func (t *Tracker) Count(p int) int { return t.counts[p] }

// Pending returns partition p's pending-buffer size, counts[p] - C.
func (t *Tracker) Pending(p int) int { return t.counts[p] - t.min }

// Feed records one delivered event in partition p. If the feed would push
// pending[p] (computed against the post-feed C) above maxPending, it
// returns ok=false and leaves all state unchanged. reads reports how many
// partition counters were read to compute the cut: always 1 here, proving
// C is maintained incrementally rather than by scanning all partitions.
func (t *Tracker) Feed(p int) (ok bool, reads int) {
	c := t.counts[p] // the single partition-counter read
	newMin := t.min
	if c == t.min && t.freq[t.min] == 1 {
		// p was the unique minimum partition; C rises with it.
		newMin++
	}
	if c+1-newMin > t.maxPending {
		return false, 1
	}
	t.freq[c]--
	t.counts[p] = c + 1
	t.freq[c+1]++
	t.min = newMin
	return true, 1
}
