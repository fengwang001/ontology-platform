// Package comp merges segments into one compacted segment. It depends
// only on seg.
package comp

import (
	"sort"

	"ontology/seg"
)

// Segment is the output of a merge: at most one record per key, sorted by
// key, tombstones garbage-collected, plus the high-water mark.
type Segment struct {
	Records   []seg.Record
	Watermark int64
}

// Merger merges segments. cmps counts the version comparisons the most
// recent Merge needed to pick per-key winners; it is deliberately
// unexported and unreachable through any exported API.
type Merger struct {
	cmps int
}

func NewMerger() *Merger { return &Merger{} }

// Merge combines any number of segments (list order irrelevant) into one
// Segment: per key the globally newest record wins, tombstone winners are
// dropped, output is sorted by key, Watermark is the max input version.
func (m *Merger) Merge(segs ...[]seg.Record) *Segment {
	m.cmps = 0
	latest := make(map[string]seg.Record)
	var maxVer int64
	for _, s := range segs {
		for _, r := range s {
			if r.Version > maxVer {
				maxVer = r.Version
			}
			cur, ok := latest[r.Key]
			if !ok {
				latest[r.Key] = r
				continue
			}
			m.cmps++ // one comparison against the per-key latest pointer
			if r.Newer(cur) {
				latest[r.Key] = r
			}
		}
	}
	out := &Segment{Watermark: maxVer}
	for _, r := range latest {
		if r.Tombstone() {
			continue // tombstone garbage-collected
		}
		out.Records = append(out.Records, r)
	}
	sort.Slice(out.Records, func(i, j int) bool {
		return out.Records[i].Key < out.Records[j].Key
	})
	return out
}
