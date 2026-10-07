package bitemporal

import "sort"

// AuditView is the independent audit reader. Unlike the export view it never
// collapses corrections: every recorded assertion remains auditable forever
// (subject to the retention horizon), including records later corrected away.
type AuditView struct{ store *Store }

// NewAuditView creates an audit reader over s.
func NewAuditView(s *Store) *AuditView { return &AuditView{store: s} }

// History returns every record ever recorded for objectID in arrival order,
// including corrected-away records. Integrity failures are reported.
func (a *AuditView) History(objectID string) ([]Record, error) {
	return a.RecordsAt(Cutoff{T: a.store.Clock(), Seq: ^uint64(0)}, objectID)
}

// RecordsAt returns every record for objectID arrived by c.Seq, even those
// whose transaction time is in the future relative to c.T or that were later
// corrected. This view is deliberately distinct from export visibility.
func (a *AuditView) RecordsAt(c Cutoff, objectID string) ([]Record, error) {
	recs, err := a.store.objectRecords(objectID, c)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// VersionsOver returns all audit records for objectID covering valid point v
// and recorded no later than t, newest transaction time first. Corrected-away
// versions are retained.
func (a *AuditView) VersionsOver(objectID string, v, t Tick) ([]Record, error) {
	recs, err := a.store.objectRecords(objectID, Cutoff{T: t, Seq: ^uint64(0)})
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, r := range recs {
		if r.TxTime <= t && r.Interval().Contains(v) {
			out = append(out, *r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TxTime != out[j].TxTime {
			return out[i].TxTime > out[j].TxTime
		}
		return out[i].Seq > out[j].Seq
	})
	return out, nil
}
