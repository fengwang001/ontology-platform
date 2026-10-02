package ontology

import "sort"

func intervalsOverlap(aStart, aEnd, bStart, bEnd int64) bool {
	return aStart < bEnd && bStart < aEnd
}

func intersectsAny(list []VMA, start, end int64) bool {
	for _, v := range list {
		if intervalsOverlap(v.Start, v.End, start, end) {
			return true
		}
	}
	return false
}

func clipVMA(v VMA, start, end int64) []VMA {
	parts := []VMA{}
	if v.Start < start {
		left := v
		left.End = start
		parts = append(parts, left)
	}
	overlapStart := maxInt64(v.Start, start)
	overlapEnd := minInt64(v.End, end)
	if overlapStart < overlapEnd {
		middle := v
		middle.Start, middle.End = overlapStart, overlapEnd
		if !middle.Anonymous {
			middle.Source.Off += overlapStart - v.Start
		}
		parts = append(parts, middle)
	}
	if v.End > end {
		right := v
		right.Start = end
		if !right.Anonymous {
			right.Source.Off += end - v.Start
		}
		parts = append(parts, right)
	}
	return parts
}

func removeRange(list []VMA, start, end int64) (result []VMA, splits int, pages int64, affected bool) {
	result = make([]VMA, 0, len(list)+2)
	for _, v := range list {
		if !intervalsOverlap(v.Start, v.End, start, end) {
			result = append(result, v)
			continue
		}
		affected = true
		if v.Start < start {
			left := v
			left.End = start
			result = append(result, left)
			splits++
		}
		overlapStart := maxInt64(v.Start, start)
		overlapEnd := minInt64(v.End, end)
		pages += overlapEnd - overlapStart
		if v.Start >= start && v.End <= end {
			continue
		}
		if v.End > end {
			right := v
			right.Start = end
			if !right.Anonymous {
				right.Source.Off += end - v.Start
			}
			result = append(result, right)
			splits++
		}
	}
	return result, splits, pages, affected
}

func compatible(a, b VMA) bool {
	if a.End != b.Start || a.Perm != b.Perm || a.GrowsDown != b.GrowsDown {
		return false
	}
	if a.Anonymous && b.Anonymous {
		return true
	}
	if a.Anonymous != b.Anonymous {
		return false
	}
	return a.Source.File == b.Source.File && b.Source.Off == a.Source.Off+a.End-a.Start
}

func mergeInserted(list []VMA, inserted VMA) ([]VMA, int) {
	all := append(append([]VMA{}, list...), inserted)
	sort.Slice(all, func(i, j int) bool { return all[i].Start < all[j].Start })
	idx := 0
	for i, v := range all {
		if v.Start == inserted.Start {
			idx = i
			break
		}
	}
	merges := 0
	if idx > 0 && compatible(all[idx-1], all[idx]) {
		all[idx].Start = all[idx-1].Start
		all[idx].Anonymous = all[idx-1].Anonymous
		if all[idx-1].Anonymous {
			all[idx].Source = Source{}
		} else {
			all[idx].Source = all[idx-1].Source
		}
		all[idx-1] = VMA{}
		merges++
	}

	compact := make([]VMA, 0, len(all)-merges)
	for _, v := range all {
		if v.End != 0 {
			compact = append(compact, v)
		}
	}
	candidate := -1
	for i := range compact {
		if compact[i].Start <= inserted.Start {
			candidate = i
		}
	}
	if candidate >= 0 {
		scan := candidate + 1
		for scan < len(compact) {
			if !compatible(compact[candidate], compact[scan]) {
				break
			}
			compact[candidate].End = compact[scan].End
			compact[scan] = VMA{}
			merges++
			scan++
		}
	}
	result := make([]VMA, 0, len(compact))
	for _, v := range compact {
		if v.End != 0 {
			result = append(result, v)
		}
	}
	return result, merges
}

func mergeWindow(list []VMA, low, high int64) []VMA {
	result := make([]VMA, 0, len(list))
	for _, v := range list {
		if len(result) > 0 {
			last := &result[len(result)-1]
			if last.Start < high && v.End > low && last.End == v.Start && compatible(*last, v) {
				last.End = v.End
				continue
			}
		}
		result = append(result, v)
	}
	return result
}

func coveragePages(list []VMA, start, end int64) int64 {
	total := int64(0)
	for _, v := range list {
		if intervalsOverlap(v.Start, v.End, start, end) {
			total += minInt64(v.End, end) - maxInt64(v.Start, start)
		}
	}
	return total
}

func findInList(list []VMA, addr int64) (VMA, bool) {
	for _, v := range list {
		if v.Start <= addr && addr < v.End {
			return v, true
		}
	}
	return VMA{}, false
}

func predecessor(list []VMA, addr int64) (VMA, bool) {
	result, found := VMA{}, false
	for _, v := range list {
		if v.Start >= addr {
			break
		}
		result, found = v, true
	}
	return result, found
}

func successor(list []VMA, addr int64) (VMA, bool) {
	for _, v := range list {
		if v.Start > addr {
			return v, true
		}
	}
	return VMA{}, false
}
