package snippet

import "sort"

// naiveSnippets is an independent round-by-round transcription of the
// selection specification, used as the differential-testing oracle.
func naiveSnippets(hits []Interval, W, K, textLen int) []Snippet {
	avail := dedupCopy(hits)
	var windows []Interval
	var result []Snippet

	for len(result) < K && len(avail) > 0 {
		bestA, bestCount, bestE := -1, 0, -1
		starts := distinctStarts(avail)
		for _, a := range starts {
			r := a + W
			if r > textLen {
				r = textLen
			}
			for _, w := range windows {
				if w.Start > a && w.Start < r {
					r = w.Start
				}
			}
			var covered []Interval
			for _, h := range avail {
				if h.Start >= a && h.End <= r {
					covered = append(covered, h)
				}
			}
			if len(covered) > bestCount {
				bestA, bestCount, bestE = a, len(covered), maxEnd(covered)
			}
		}
		if bestCount == 0 {
			break
		}
		r := bestA + W
		if r > textLen {
			r = textLen
		}
		for _, w := range windows {
			if w.Start > bestA && w.Start < r {
				r = w.Start
			}
		}
		var covered []Interval
		for _, h := range avail {
			if h.Start >= bestA && h.End <= r {
				covered = append(covered, h)
			}
		}
		window := Interval{Start: bestA, End: bestE}
		windows = append(windows, window)
		result = append(result, Snippet{
			Start:      bestA,
			End:        bestE,
			Highlights: naiveMerge(covered),
			Score:      bestCount,
		})
		var next []Interval
		for _, h := range avail {
			if !(h.Start < window.End && window.Start < h.End) {
				next = append(next, h)
			}
		}
		avail = next
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Start < result[j].Start })
	return result
}

func dedupCopy(hits []Interval) []Interval {
	s := append([]Interval(nil), hits...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		return s[i].End < s[j].End
	})
	var out []Interval
	for _, h := range s {
		if len(out) == 0 || out[len(out)-1] != h {
			out = append(out, h)
		}
	}
	return out
}

func distinctStarts(hits []Interval) []int {
	seen := map[int]bool{}
	var starts []int
	for _, h := range hits {
		if !seen[h.Start] {
			seen[h.Start] = true
			starts = append(starts, h.Start)
		}
	}
	sort.Ints(starts)
	return starts
}

func maxEnd(hits []Interval) int {
	m := -1
	for _, h := range hits {
		if h.End > m {
			m = h.End
		}
	}
	return m
}

func naiveMerge(hits []Interval) []Interval {
	s := append([]Interval(nil), hits...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		return s[i].End < s[j].End
	})
	var out []Interval
	for _, h := range s {
		if n := len(out); n > 0 && h.Start < out[n-1].End {
			if h.End > out[n-1].End {
				out[n-1].End = h.End
			}
		} else {
			out = append(out, h)
		}
	}
	return out
}
