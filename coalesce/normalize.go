package coalesce

func (n *Normalizer) Normalize(in []Interval) []Interval {
	n.comparisons = 0
	if len(in) == 0 {
		return nil
	}
	sorted := make([]Interval, len(in))
	copy(sorted, in)
	n.mergeSort(sorted)

	out := make([]Interval, 0, len(sorted))
	cur := sorted[0]
	for _, next := range sorted[1:] {
		n.comparisons++
		// Sorted order guarantees next.Start >= cur.Start. Intervals merge
		// when they overlap (next.Start <= cur.End) or are exactly adjacent
		// (next.Start == cur.End+1): the covered byte set is unchanged, and a
		// single merged interval must later degrade to a bare body.
		if next.Start <= cur.End+1 {
			if next.End > cur.End {
				cur.End = next.End
			}
			continue
		}
		out = append(out, cur)
		cur = next
	}
	out = append(out, cur)
	return out
}

// mergeSort is an explicit top-down merge sort so the comparison count is
// deterministic. Every binary "which interval comes first" decision goes
// through less(), which increments the counter exactly once.
func (n *Normalizer) mergeSort(a []Interval) {
	if len(a) < 2 {
		return
	}
	mid := len(a) / 2
	left := append([]Interval(nil), a[:mid]...)
	right := append([]Interval(nil), a[mid:]...)
	n.mergeSort(left)
	n.mergeSort(right)
	n.merge(a, left, right)
}

func (n *Normalizer) merge(dst, left, right []Interval) {
	i, j := 0, 0
	for k := range dst {
		switch {
		case i >= len(left):
			dst[k] = right[j]
			j++
		case j >= len(right):
			dst[k] = left[i]
			i++
		default:
			if n.less(left[i], right[j]) {
				dst[k] = left[i]
				i++
			} else {
				dst[k] = right[j]
				j++
			}
		}
	}
}

// less orders by Start, breaking ties by End. One call, one counted compare.
func (n *Normalizer) less(a, b Interval) bool {
	n.comparisons++
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.End < b.End
}
