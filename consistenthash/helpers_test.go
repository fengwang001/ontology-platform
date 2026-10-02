package consistenthash

// bisectBoundForTest exposes the proven comparison bound for tests.
func bisectBoundForTest(p int) int { return bisectBound(p) }

// locateStartForTest drives a single binary search and returns its index.
func (r *Ring) locateStartForTest(p uint64) int { return r.locateStart(p) }

// resetBisectForTest clears the instrumentation counters.
func (r *Ring) resetBisectForTest() {
	r.bisectTotal = 0
	r.bisectMax = 0
}

// pointsCountForTest reports the number of ring points currently registered.
func (r *Ring) pointsCountForTest() int { return len(r.points) }
