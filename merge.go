package ontology

import "math"

// Union, Intersect and Difference operate directly on the compressed run
// lists: two cursors walk the inputs and emit canonical output runs. No
// expansion to a dense bitmap, []bool or big.Int ever happens, so cost is
// O(runs), not O(bits). Each returns the number of run-advance steps the
// merge performed, so callers can observe that cost directly.

// Union returns the set union and the number of runs consumed.
func (s *Set) Union(o *Set) (result *Set, steps int) {
	runs, n := unionRuns(s.snapshot(), o.snapshot())
	return &Set{runs: runs}, n
}

// Intersect returns the set intersection and the number of merge steps.
func (s *Set) Intersect(o *Set) (result *Set, steps int) {
	runs, n := intersectRuns(s.snapshot(), o.snapshot())
	return &Set{runs: runs}, n
}

// Difference returns the set s \ o and the number of merge steps.
func (s *Set) Difference(o *Set) (result *Set, steps int) {
	runs, n := differenceRuns(s.snapshot(), o.snapshot())
	return &Set{runs: runs}, n
}

// unionRuns merges two canonical run lists into one canonical list.
// steps counts every input run consumed from either list.
func unionRuns(a, b []interval) (out []interval, steps int) {
	i, j := 0, 0
	var cur interval
	open := false
	for i < len(a) || j < len(b) {
		var next interval
		if j >= len(b) || (i < len(a) && a[i].start <= b[j].start) {
			next = a[i]
			i++
		} else {
			next = b[j]
			j++
		}
		steps++
		// Absorb next into cur when they overlap or touch. The
		// cur.end == MaxUint32 guard avoids the cur.end+1 overflow;
		// such a cur absorbs everything anyway.
		if open && (cur.end == math.MaxUint32 || next.start <= cur.end+1) {
			if next.end > cur.end {
				cur.end = next.end
			}
			continue
		}
		if open {
			out = append(out, cur)
		}
		cur, open = next, true
	}
	if open {
		out = append(out, cur)
	}
	return out, steps
}

// intersectRuns intersects two canonical run lists. Output runs stay
// canonical: consecutive outputs derive from distinct input runs, which
// themselves keep a gap of at least one zero bit.
func intersectRuns(a, b []interval) (out []interval, steps int) {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		steps++
		lo := max(a[i].start, b[j].start)
		hi := min(a[i].end, b[j].end)
		if lo <= hi {
			out = append(out, interval{lo, hi})
		}
		if a[i].end < b[j].end {
			i++
		} else {
			j++
		}
	}
	return out, steps
}

// differenceRuns computes a \ b on canonical run lists.
func differenceRuns(a, b []interval) (out []interval, steps int) {
	j := 0
	for _, r := range a {
		steps++
		cur := r
		for j < len(b) && b[j].end < cur.start {
			j++
			steps++
		}
		k := j
		remaining := true
		for k < len(b) && b[k].start <= cur.end {
			steps++
			if b[k].start > cur.start {
				out = append(out, interval{cur.start, b[k].start - 1})
			}
			if b[k].end >= cur.end {
				remaining = false
				break
			}
			cur.start = b[k].end + 1 // safe: b[k].end < cur.end <= MaxUint32
			k++
		}
		if remaining {
			out = append(out, cur)
		}
	}
	return out, steps
}
