// Package merge 将多个段一次多路归并为一个段，清除已删除文档。
package merge

import (
	"ontology/posting"
	"ontology/segment"
)

// Stats 统计合并过程中的倒排项读写次数。
type Stats struct {
	Reads  int
	Writes int
}

// Merge 对 segs 做一次多路归并：同词的链按文档号归并，
// deleted 中的文档倒排项被清除。每条倒排项只读一次、写一次。
func Merge(segs []*segment.Segment, deleted map[uint32]bool, st *Stats) *segment.Segment {
	ptr := make([]int, len(segs))
	out := map[string]posting.List{}
	for {
		minTerm := ""
		found := false
		for i, s := range segs {
			if ptr[i] >= len(s.Terms) {
				continue
			}
			if t := s.Terms[ptr[i]]; !found || t < minTerm {
				minTerm, found = t, true
			}
		}
		if !found {
			break
		}
		var lists []posting.List
		for i, s := range segs {
			if ptr[i] < len(s.Terms) && s.Terms[ptr[i]] == minTerm {
				lists = append(lists, s.Lists[minTerm])
				ptr[i]++
			}
		}
		if merged := mergeLists(lists, deleted, st); len(merged) > 0 {
			out[minTerm] = merged
		}
	}
	return segment.New(out)
}

// mergeLists 按文档号归并同词的多条链；同文档位置归并去重。
func mergeLists(lists []posting.List, deleted map[uint32]bool, st *Stats) posting.List {
	ptr := make([]int, len(lists))
	var out posting.List
	for {
		minDoc := ^uint32(0)
		found := false
		for i, l := range lists {
			if ptr[i] < len(l) {
				if d := l[ptr[i]].Doc; !found || d < minDoc {
					minDoc, found = d, true
				}
			}
		}
		if !found {
			return out
		}
		var pos []uint32
		for i, l := range lists {
			if ptr[i] < len(l) && l[ptr[i]].Doc == minDoc {
				st.Reads++
				pos = mergePositions(pos, l[ptr[i]].Pos)
				ptr[i]++
			}
		}
		if deleted[minDoc] {
			continue // 已删除文档：读但不写
		}
		st.Writes++
		out = append(out, posting.Posting{Doc: minDoc, Pos: pos})
	}
}

// mergePositions 归并两条升序位置表并去重。
func mergePositions(a, b []uint32) []uint32 {
	out := make([]uint32, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		case a[i] > b[j]:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}
