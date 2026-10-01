package duty

import "sort"

// Timeline returns the maximal contiguous segments covering [a, b).
//
// Override endpoints inside [a,b) are processed in sorted order. A max-seq
// heap holds the overrides active at the sweep point; expired overrides leave
// lazily when they reach the top. While no override is active, the only other
// place the answer can change is a rotation boundary. Each emitted piece is
// merged with the previous one only when both member and source match, so the
// result partitions [a, b), every adjacent pair differs in member or source,
// and Who(t) for any t inside a piece agrees with it. A merged result larger
// than 10000 pieces rejects the whole query without side effects.
func (s *Schedule) Timeline(a, b int64) ([]Segment, error) {
	if err := validateInterval(a, b); err != nil {
		return nil, err
	}

	type startsAt struct {
		at int64
		id int
	}
	entries := s.snapshot()
	events := make([]startsAt, 0, 2*len(entries))
	for i := range entries {
		o := entries[i].ov
		if o.End <= a || o.Start >= b {
			continue
		}
		if o.Start >= a && o.Start < b {
			events = append(events, startsAt{at: o.Start, id: i})
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })

	// active holds the overrides covering the sweep point, kept ordered with the
	// newest (highest seq) first; expired entries are removed at each boundary.
	active := make([]int, 0, len(entries))
	less := func(i, j int) bool { return entries[active[i]].seq > entries[active[j]].seq }

	// Seed overrides already active at a.
	for i := range entries {
		if o := entries[i].ov; o.Start <= a && a < o.End {
			active = append(active, i)
		}
	}
	sort.SliceStable(active, less)

	var segs []Segment
	emit := func(start, end int64) {
		member, source := s.rotationMember(start), "rotation"
		if len(active) > 0 {
			o := entries[active[0]].ov
			member, source = o.Member, o.ID
		}
		if n := len(segs); n > 0 &&
			segs[n-1].End == start &&
			segs[n-1].Member == member && segs[n-1].Source == source {
			segs[n-1].End = end
		} else {
			segs = append(segs, Segment{Start: start, End: end, Member: member, Source: source})
		}
	}

	cur := a
	evIdx := 0
	for cur < b {
		// Boundary candidates strictly after cur.
		next := b
		if len(active) == 0 {
			if rb := s.nextRotationBoundary(cur); rb < next {
				next = rb
			}
		} else {
			if e := entries[active[0]].ov.End; e < next {
				next = e
			}
		}
		for evIdx < len(events) && events[evIdx].at <= cur {
			evIdx++
		}
		if evIdx < len(events) && events[evIdx].at < next {
			next = events[evIdx].at
		}
		emit(cur, next)
		if len(segs) > maxSegments {
			return nil, ErrTimelineTooLarge
		}

		cur = next
		// Start/end events at cur take effect for the piece beginning at cur.
		for evIdx < len(events) && events[evIdx].at == cur {
			active = append(active, events[evIdx].id)
			evIdx++
		}

		filtered := active[:0]
		for _, id := range active {
			if entries[id].ov.End > cur {
				filtered = append(filtered, id)
			}
		}
		active = filtered
		sort.SliceStable(active, less)
	}
	return segs, nil
}

// nextRotationBoundary returns the smallest T0+kL strictly greater than t.
func (s *Schedule) nextRotationBoundary(t int64) int64 {
	delta := t - s.t0
	q := delta / s.length
	r := delta % s.length
	if r < 0 {
		q--
	}
	return s.t0 + (q+1)*s.length
}
