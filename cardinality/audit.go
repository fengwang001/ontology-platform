package cardinality

import "time"

func (s *store) audit(kind EventKind, key BucketKey, linkID string, reason Reason, basis *MarkBasis, outcome, detail string) {
	s.auditLog = append(s.auditLog, AuditRecord{
		Seq:     s.seq,
		At:      time.Now(),
		Kind:    kind,
		Bucket:  key,
		LinkID:  linkID,
		Reason:  reason,
		Basis:   basis,
		Outcome: outcome,
		Detail:  detail,
	})
}

func cloneBasis(b MarkBasis) *MarkBasis {
	cp := b
	if b.OrderedIDs != nil {
		cp.OrderedIDs = append([]string(nil), b.OrderedIDs...)
	}
	return &cp
}
