package bitmap

// Count returns the number of elements in the set. It sums the
// lengths of the 1-runs only, so its cost is O(number of runs),
// never O(number of bits).
func (b *Bitmap) Count() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	var total uint64
	for _, r := range b.runs {
		if r.val == 1 {
			total += r.length
		}
	}
	b.lastStatRuns = len(b.runs)
	return total
}

// Min returns the smallest element in the set. ok is false when the
// set is empty. Cost is O(runs before the first 1-run), not O(bits).
func (b *Bitmap) Min() (min uint32, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var pos uint64
	for i, r := range b.runs {
		if r.val == 1 {
			b.lastStatRuns = i + 1
			return uint32(pos), true
		}
		pos += r.length
	}
	b.lastStatRuns = len(b.runs)
	return 0, false
}

// Max returns the largest element in the set. ok is false when the
// set is empty. The last run is always a 1-run (canonical form), so
// Max inspects exactly one run.
func (b *Bitmap) Max() (max uint32, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastStatRuns = 0
	if len(b.runs) == 0 {
		return 0, false
	}
	b.lastStatRuns = 1
	return uint32(b.totalLen - 1), true
}

// LastStatRuns reports how many runs the most recent Count, Min or
// Max call inspected. It demonstrates that statistics are computed
// in O(runs), not O(bits).
func (b *Bitmap) LastStatRuns() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastStatRuns
}
