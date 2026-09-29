package pitr

// region is the half-open position interval [Lo, Hi) on which Timeline is
// the effective timeline of the target timeline.
type region struct {
	timeline TimelineID
	lo       Position
	hi       Position
}

// ancestorChain returns the target timeline's ancestor chain ordered from
// the root timeline to the target. Caller must hold at least a read lock.
// The second result is false when the target timeline is not registered.
func (s *Store) ancestorChain(target TimelineID) ([]Timeline, bool) {
	chain := make([]Timeline, 0, 4)
	cur, ok := s.timelines[target]
	if !ok {
		return nil, false
	}
	for {
		chain = append(chain, cur)
		if cur.ID == InitialTimeline {
			break
		}
		parent, ok := s.timelines[cur.Parent]
		if !ok {
			return nil, false
		}
		cur = parent
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, true
}

// chainRegions converts an ancestor chain (root first) into effective
// regions. The final region reaches +1<<62 (positions are non-negative).
func chainRegions(chain []Timeline) []region {
	const infinity = Position(1) << 62
	regions := make([]region, len(chain))
	for i, tl := range chain {
		r := region{timeline: tl.ID, lo: tl.Fork, hi: infinity}
		if i+1 < len(chain) {
			r.hi = chain[i+1].Fork
		}
		regions[i] = r
	}
	return regions
}

// mergeIntervals merges overlapping or touching half-open intervals and
// clips every interval to [lo, hi). Input must be sorted by Start.
func mergeIntervals(segs []Segment, lo, hi Position) []PlanStep {
	if hi <= lo {
		return nil
	}
	steps := make([]PlanStep, 0, len(segs))
	for _, seg := range segs {
		start := seg.Start
		if start < lo {
			start = lo
		}
		end := seg.End
		if end > hi {
			end = hi
		}
		if end <= start {
			continue
		}
		if n := len(steps); n > 0 && steps[n-1].End >= start {
			if end > steps[n-1].End {
				steps[n-1].End = end
			}
			continue
		}
		steps = append(steps, PlanStep{Timeline: seg.Timeline, Start: start, End: end})
	}
	return steps
}
