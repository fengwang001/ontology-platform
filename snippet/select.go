package snippet

import "sort"

// selectSnippets runs the fixed-window greedy rounds over deduplicated hits.
//
// Each round:
//  1. Available hits are those disjoint from every selected window
//     (intersection means s < selectedEnd && selectedStart < e).
//  2. For each distinct available hit start a, the window right edge is
//     r(a) = min(a+W, textLen, smallest start of a selected window that
//     begins after a). The covered set C(a) is every available hit with
//     s >= a and e <= r(a); e == r(a) is included.
//  3. Score is |C(a)| (overlapping distinct hits count separately). The
//     maximum-score a wins; ties take the smallest a. Maximum score 0 ends
//     the rounds.
//  4. The window becomes [a, E) with E = max e in C(a).
//
// Counts are maintained with a Fenwick tree over hit end offsets while two
// monotone pointers slide over starts sorted by (s, e): r(a) is
// non-decreasing in a, so the whole round is O(m log m) for m available
// hits.
func selectSnippets(hits []Interval, W, K, textLen int) []Snippet {
	var selected []Snippet
	selectedWin := make([]Interval, 0, K)
	available := append([]Interval(nil), hits...)

	for len(selected) < K && len(available) > 0 {
		// End offsets in ascending order for Fenwick rank queries.
		ends := make([]int, len(available))
		for i, h := range available {
			ends[i] = h.End
		}
		sort.Ints(ends)

		bit := newFenwick(len(ends))
		for i := range available {
			bit.add(sort.SearchInts(ends, available[i].End)+1, 1)
		}

		bestA, bestScore, bestE := -1, 0, -1
		p1, p2 := 0, 0

		for idx := 0; idx < len(available); {
			a := available[idx].Start

			r := a + W
			if r > textLen {
				r = textLen
			}
			if block := nextSelectedStart(selectedWin, a); block >= 0 && block < r {
				r = block
			}

			for p1 < len(available) && available[p1].Start < a {
				bit.add(sort.SearchInts(ends, available[p1].End)+1, -1)
				p1++
			}
			for p2 < len(available) && available[p2].Start < r {
				p2++
			}

			// End offsets are integers: end <= r iff end < r+1.
			score := bit.prefix(sort.SearchInts(ends, r+1))
			if score > bestScore {
				eMax := 0
				for _, h := range available[p1:p2] {
					if h.End <= r && h.End > eMax {
						eMax = h.End
					}
				}
				bestA, bestScore, bestE = a, score, eMax
			}

			for idx < len(available) && available[idx].Start == a {
				idx++
			}
		}

		if bestScore == 0 {
			break
		}

		r := bestA + W
		if r > textLen {
			r = textLen
		}
		if block := nextSelectedStart(selectedWin, bestA); block >= 0 && block < r {
			r = block
		}
		covered := make([]Interval, 0, bestScore)
		for _, h := range available {
			if h.Start >= bestA && h.Start < r && h.End <= r {
				covered = append(covered, h)
			}
		}

		window := Interval{Start: bestA, End: bestE}
		selectedWin = append(selectedWin, window)
		selected = append(selected, Snippet{
			Start:      bestA,
			End:        bestE,
			Highlights: mergeOverlaps(covered),
			Score:      bestScore,
		})

		next := available[:0]
		for _, h := range available {
			if !intersects(h, window) {
				next = append(next, h)
			}
		}
		available = append([]Interval(nil), next...)
		sort.Slice(available, func(i, j int) bool {
			if available[i].Start != available[j].Start {
				return available[i].Start < available[j].Start
			}
			return available[i].End < available[j].End
		})
	}

	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Start < selected[j].Start
	})
	for i := range selected {
		hl := make([]Interval, len(selected[i].Highlights))
		copy(hl, selected[i].Highlights)
		selected[i].Highlights = hl
	}
	return selected
}

// nextSelectedStart returns the smallest selected window start strictly
// greater than a, or -1.
func nextSelectedStart(windows []Interval, a int) int {
	closest := -1
	for _, w := range windows {
		if w.Start > a && (closest == -1 || w.Start < closest) {
			closest = w.Start
		}
	}
	return closest
}

// intersects reports whether [s,e) and window share any point:
// s < windowEnd && windowStart < e. Touching intervals do not intersect.
func intersects(h, window Interval) bool {
	return h.Start < window.End && window.Start < h.End
}

// mergeOverlaps merges intervals sorted by start using the strict rule
// s2 < e1 (touching intervals stay separate).
func mergeOverlaps(hits []Interval) []Interval {
	if len(hits) == 0 {
		return nil
	}
	sorted := append([]Interval(nil), hits...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	merged := make([]Interval, 0, len(sorted))
	cur := sorted[0]
	for _, h := range sorted[1:] {
		if h.Start < cur.End {
			if h.End > cur.End {
				cur.End = h.End
			}
			continue
		}
		merged = append(merged, cur)
		cur = h
	}
	merged = append(merged, cur)
	return merged
}

// fenwick is a 1-indexed binary indexed tree.
type fenwick struct {
	tree []int
}

func newFenwick(n int) *fenwick {
	return &fenwick{tree: make([]int, n+1)}
}

func (f *fenwick) add(i, delta int) {
	for ; i < len(f.tree); i += i & -i {
		f.tree[i] += delta
	}
}

// prefix returns the sum over indices [1, i]; i <= 0 yields 0.
func (f *fenwick) prefix(i int) int {
	sum := 0
	for ; i > 0; i -= i & -i {
		sum += f.tree[i]
	}
	return sum
}
