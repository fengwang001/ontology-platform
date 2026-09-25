// Package comp merges segments: per key it keeps the globally
// highest-version record, garbage-collects tombstones, emits records
// sorted by key and reports a watermark. It depends only on seg.
package comp

import (
	"errors"
	"sort"

	"ontology/seg"
)

// Segment is records ordered by ascending Version plus the watermark
// marking the version up to which it has been fully compacted.
type Segment struct {
	Records   []seg.Rec
	Watermark int64
}

// ErrSeekCost marks a failed structural pointer-seek probe.
var ErrSeekCost = errors.New("comp: winner lookup must use one pointer per key")

// Merger compacts segments. cmpCount is the non-exported count of
// version comparisons performed by the latest Compact to determine each
// key's winning record; it is never reachable from any exported API.
type Merger struct {
	cmpCount int
}

// NewMerger returns a ready Merger.
func NewMerger() *Merger { return &Merger{} }

// Compact merges the given segments (any order) into one segment: per
// key at most one record survives (the globally highest-version one),
// tombstoned keys disappear, output is key-sorted, and the watermark is
// the maximum version across every input record. Malformed input fails
// the whole call and leaves no trace.
func (m *Merger) Compact(segs []*Segment) (*Segment, error) {
	// Validate everything before touching any state.
	for _, s := range segs {
		for _, r := range s.Records {
			if err := r.Valid(); err != nil {
				m.cmpCount = 0
				return nil, err
			}
		}
	}

	// One pointer per key to its current winning record; every
	// contender costs exactly one version comparison against it.
	latest := make(map[string]*seg.Rec)
	var cmps int
	var wm int64
	for _, s := range segs {
		for i := range s.Records {
			r := &s.Records[i]
			if r.Version > wm {
				wm = r.Version
			}
			cur, ok := latest[r.Key]
			if !ok {
				latest[r.Key] = r
				continue
			}
			cmps++
			if seg.Newer(r.Version, cur.Version) {
				latest[r.Key] = r
			}
		}
	}

	keys := make([]string, 0, len(latest))
	for k, r := range latest {
		if !r.IsTombstone() {
			keys = append(keys, k) // tombstone garbage-collected
		}
	}
	sort.Strings(keys)

	out := &Segment{Watermark: wm}
	for _, k := range keys {
		out.Records = append(out.Records, *latest[k])
	}
	m.cmpCount = cmps
	return out, nil
}

// VerifyPointerSeek feeds, for each history size, m puts into one
// compaction (whose output keeps a single record for the key) and then
// compacts one more put against it; locating the winner must stay a
// constant number of comparisons independent of m. It returns only a
// pass/fail verdict, never any count.
func (m *Merger) VerifyPointerSeek(sizes []int) error {
	for _, n := range sizes {
		hist := make([]seg.Rec, n)
		for i := range hist {
			hist[i] = seg.Rec{Key: "k", Version: int64(i + 1), Op: seg.OpPut, Val: "v"}
		}
		base, err := m.Compact([]*Segment{{Records: hist}})
		if err != nil {
			return err
		}
		next := []seg.Rec{{Key: "k", Version: int64(n + 1), Op: seg.OpPut, Val: "w"}}
		if _, err := m.Compact([]*Segment{base, {Records: next}}); err != nil {
			return err
		}
		if m.cmpCount > 2 {
			return ErrSeekCost
		}
	}
	return nil
}
