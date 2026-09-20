package byterange

import "sort"

// resolveAll converts raw specs into absolute ranges clipped to
// [0, size-1], silently dropping the unsatisfiable ones.
func resolveAll(specs []spec, size int64) []Range {
	ranges := make([]Range, 0, len(specs))
	for _, s := range specs {
		if r, ok := resolve(s, size); ok {
			ranges = append(ranges, r)
		}
	}
	return ranges
}

// resolve maps one spec to an absolute closed interval.
func resolve(s spec, size int64) (Range, bool) {
	if size == 0 {
		return Range{}, false
	}
	if s.suffix {
		if s.length == 0 {
			return Range{}, false // "-0" is unsatisfiable
		}
		start := int64(0)
		if s.length < size {
			start = size - s.length
		}
		return Range{Start: start, End: size - 1}, true
	}
	if s.start >= size {
		return Range{}, false // starts past the end of the resource
	}
	end := s.end
	if end < 0 || end >= size {
		end = size - 1
	}
	return Range{Start: s.start, End: end}, true
}

// merge sorts ranges by start and coalesces overlapping or
// adjacent intervals (prev.End+1 >= next.Start).
func merge(ranges []Range) []Range {
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].Start < ranges[j].Start
	})
	out := ranges[:0]
	for _, r := range ranges {
		last := len(out) - 1
		if last >= 0 && r.Start <= out[last].End+1 {
			if r.End > out[last].End {
				out[last].End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}
