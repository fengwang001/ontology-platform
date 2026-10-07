package bitemporal

import "sort"

// NaiveModel is an independent, deliberately simple bi-temporal reference
// implementation used only for differential testing. It answers every query by
// scanning every record it has ever ingested, so it is trivially correct but
// O(N); the production exporter must agree with it on all randomized inputs.
type NaiveModel struct {
	recs []Record
}

// NewNaiveModel constructs an empty reference model.
func NewNaiveModel() *NaiveModel { return &NaiveModel{} }

// Put ingests one record. Callers feed the exact same operation sequence that
// was applied to the production store.
func (m *NaiveModel) Put(r Record) { m.recs = append(m.recs, r) }

// PointVisible returns the record visible at (objectID, v) as of cutoff c, or
// nil when the point is unknown. Linear scan over all ingested records.
func (m *NaiveModel) PointVisible(objectID string, v Tick, c Cutoff) *Record {
	var best *Record
	for i := range m.recs {
		r := &m.recs[i]
		if r.ObjectID != objectID || r.Seq > c.Seq || r.TxTime > c.T {
			continue
		}
		if !r.Interval().Contains(v) {
			continue
		}
		if best == nil || r.TxTime > best.TxTime || (r.TxTime == best.TxTime && r.Seq > best.Seq) {
			best = r
		}
	}
	return best
}

// NaiveSegment is one constant run in the naive timeline snapshot.
type NaiveSegment struct {
	Start Tick
	End   Tick
	Seq   uint64 // 0 means unknown
}

// TimelineVisible computes the visible sequence for every distinct boundary in
// [lo, hi) via independent point queries, returning the canonical segments.
func (m *NaiveModel) TimelineVisible(objectID string, span Interval, c Cutoff) []NaiveSegment {
	bounds := map[Tick]struct{}{span.Start: {}, span.End: {}}
	for _, r := range m.recs {
		if r.ObjectID != objectID {
			continue
		}
		if r.Start >= span.Start && r.Start < span.End {
			bounds[r.Start] = struct{}{}
		}
		if r.End > span.Start && r.End <= span.End {
			bounds[r.End] = struct{}{}
		}
	}
	pts := make([]Tick, 0, len(bounds))
	for b := range bounds {
		pts = append(pts, b)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })

	var out []NaiveSegment
	for i := 0; i+1 < len(pts); i++ {
		start, end := pts[i], pts[i+1]
		if start < span.Start || end > span.End {
			continue
		}
		seq := uint64(0)
		if r := m.PointVisible(objectID, start, c); r != nil {
			seq = r.Seq
		}
		if n := len(out); n > 0 && out[n-1].End == start && out[n-1].Seq == seq {
			out[n-1].End = end
			continue
		}
		out = append(out, NaiveSegment{Start: start, End: end, Seq: seq})
	}
	return out
}
