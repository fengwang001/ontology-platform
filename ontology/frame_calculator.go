package ontology

import "math/bits"

func computeFrame(keys []int64, groups []int64, i int, spec FrameSpec) []Interval {
	baseValue := metricValue(keys, groups, spec.Mode, i)

	var intervals []Interval
	appendRow := func(row int) {
		if len(intervals) > 0 && intervals[len(intervals)-1].End == row {
			intervals[len(intervals)-1].End = row + 1
			return
		}
		intervals = append(intervals, Interval{Start: row, End: row + 1})
	}

	for row := range keys {
		value := metricValue(keys, groups, spec.Mode, row)
		if !startAllows(value, baseValue, spec.Start) || !endAllows(value, baseValue, spec.End) {
			continue
		}

		excluded := false
		switch spec.Exclude {
		case ExcludeCurrentRow:
			excluded = row == i
		case ExcludeGroup:
			excluded = groups[row] == groups[i]
		case ExcludeTies:
			excluded = groups[row] == groups[i] && row != i
		}
		if excluded {
			continue
		}

		appendRow(row)
	}
	return intervals
}

func metricValue(keys []int64, groups []int64, mode Mode, row int) int64 {
	switch mode {
	case Rows:
		return int64(row)
	case Groups:
		return groups[row]
	default:
		return keys[row]
	}
}

func startAllows(value int64, baseValue int64, bound Bound) bool {
	switch bound.Type {
	case UnboundedPreceding:
		return true
	case Preceding:
		return compareSumTo(value, bound.N, baseValue) >= 0
	case CurrentRowBound:
		return value >= baseValue
	case Following:
		return compareSumTo(baseValue, bound.N, value) <= 0
	default:
		return false
	}
}

func endAllows(value int64, baseValue int64, bound Bound) bool {
	switch bound.Type {
	case Preceding:
		return compareSumTo(value, bound.N, baseValue) <= 0
	case CurrentRowBound:
		return value <= baseValue
	case Following:
		return compareSumTo(baseValue, bound.N, value) >= 0
	default:
		return true
	}
}

func compareSumTo(a int64, b int64, target int64) int {
	unsignedSum, _ := bits.Add64(uint64(a), uint64(b), 0)
	sum := int64(unsignedSum)

	if a >= 0 && b >= 0 && sum < 0 {
		return 1
	}
	if a < 0 && b < 0 && sum >= 0 {
		return -1
	}
	if sum < target {
		return -1
	}
	if sum > target {
		return 1
	}
	return 0
}
