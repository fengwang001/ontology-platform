package coalesce

import "ontology/rangespec"

// Range is a normalized half-open byte interval [Start, End).
type Range struct {
	Start int64
	End   int64
}

// Normalize clips intervals to the resource, sorts them, and merges overlaps/adjacencies.
func Normalize(items []rangespec.Interval, length int64) ([]Range, error) {
	ranges, _, err := NormalizeCounted(items, length)
	return ranges, err
}

// NormalizeCounted behaves like Normalize and returns deterministic interval comparisons.
func NormalizeCounted(items []rangespec.Interval, length int64) ([]Range, int, error) {
	ranges := clip(items, length)
	if len(ranges) == 0 {
		return nil, 0, UnsatisfiableError{ResourceLength: length}
	}

	comparisons := 0
	mergeSort(ranges, &comparisons)

	merged := make([]Range, 0, len(ranges))
	current := ranges[0]
	for _, next := range ranges[1:] {
		comparisons++
		if next.Start <= current.End {
			if next.End > current.End {
				current.End = next.End
			}
		} else {
			merged = append(merged, current)
			current = next
		}
	}
	merged = append(merged, current)
	return merged, comparisons, nil
}

func clip(items []rangespec.Interval, length int64) []Range {
	ranges := make([]Range, 0, len(items))
	for _, item := range items {
		switch {
		case item.Start >= 0 && item.End >= 0:
			if item.Start >= length || item.Start > item.End {
				continue
			}
			end := item.End + 1
			if end > length {
				end = length
			}
			ranges = append(ranges, Range{Start: item.Start, End: end})
		case item.Start >= 0:
			if item.Start >= length {
				continue
			}
			ranges = append(ranges, Range{Start: item.Start, End: length})
		case item.End > 0:
			count := item.End
			if count >= length {
				count = length
			}
			ranges = append(ranges, Range{Start: length - count, End: length})
		}
	}
	return ranges
}

func mergeSort(values []Range, comparisons *int) {
	if len(values) < 2 {
		return
	}
	middle := len(values) / 2
	left := append([]Range(nil), values[:middle]...)
	right := append([]Range(nil), values[middle:]...)
	mergeSort(left, comparisons)
	mergeSort(right, comparisons)

	l, r := 0, 0
	for l < len(left) && r < len(right) {
		*comparisons++
		if left[l].Start <= right[r].Start {
			values[l+r] = left[l]
			l++
		} else {
			values[l+r] = right[r]
			r++
		}
	}
	for l < len(left) {
		values[l+r] = left[l]
		l++
	}
	for r < len(right) {
		values[l+r] = right[r]
		r++
	}
}
