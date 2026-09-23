// Package merge implements k-way merging of segments into one, purging
// deleted documents, plus a concurrency-safe segment set.
package merge

import (
	"ontology/posting"
	"ontology/segment"
)

// Stats counts posting entries read and written during a merge.
type Stats struct {
	ItemsRead    int
	ItemsWritten int
}

// Merge combines segs (oldest first) into a single segment written to
// outPath. deleted[i] holds the deleted doc IDs of segs[i]; those entries
// are read but not written. Every remaining entry is read once and
// written once. Doc IDs increase with segment age, so concatenating
// per-segment lists in age order keeps them sorted.
func Merge(segs []*segment.Segment, deleted []map[uint32]bool, outPath string) (Stats, error) {
	var stats Stats
	idx := make([]int, len(segs))
	merged := map[string]posting.List{}
	for {
		minTerm := ""
		found := false
		for i, seg := range segs {
			if idx[i] >= len(seg.Terms()) {
				continue
			}
			t := seg.Terms()[idx[i]]
			if !found || t < minTerm {
				minTerm = t
				found = true
			}
		}
		if !found {
			break
		}
		var out posting.List
		for i, seg := range segs {
			if idx[i] >= len(seg.Terms()) || seg.Terms()[idx[i]] != minTerm {
				continue
			}
			idx[i]++
			list, _ := seg.Postings(minTerm)
			for _, e := range list {
				stats.ItemsRead++
				if deleted[i] != nil && deleted[i][e.Doc] {
					continue
				}
				stats.ItemsWritten++
				out = append(out, e)
			}
		}
		if len(out) > 0 {
			merged[minTerm] = out
		}
	}
	return stats, segment.Write(outPath, merged)
}
