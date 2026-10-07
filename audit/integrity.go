package audit

// IntegrityProblem describes one detected tamper event for full-chain audits.
type IntegrityProblem struct {
	Kind   string
	ID     string
	Detail string
}

// verifyVersionContent checks only the self-consistency of one rule version:
// re-hashing its content and metadata. It performs no traversal, so a replay
// of one audit record reads exactly the one version named by that record.
func verifyVersionContent(v RuleVersion) error {
	wantContent, _, err := hashVersionContent(RuleVersionInput{
		ID:       v.ID,
		ParentID: v.ParentID,
		Rules:    v.Rules,
	})
	if err != nil {
		return err
	}
	if wantContent != v.ContentHash {
		return ErrVersionTampered
	}
	wantChain, err := hashVersionChain(v)
	if err != nil {
		return err
	}
	if wantChain != v.ChainHash {
		return ErrVersionTampered
	}
	return nil
}

// verifyAuditRecord re-hashes one audit record.
func verifyAuditRecord(a AuditRecord) error {
	want, err := hashAudit(a)
	if err != nil {
		return err
	}
	if want != a.RecordHash {
		return ErrAuditTampered
	}
	return nil
}

// verifyCorrection re-hashes one correction record.
func verifyCorrection(c Correction) error {
	want, err := hashCorrection(c)
	if err != nil {
		return err
	}
	if want != c.RecordHash {
		return ErrCorrectionTampered
	}
	return nil
}

// VerifyAll walks every chain in commit order and reports each broken self
// hash or chain link. It is an administrative tool; replay itself only reads
// the single named version and its own audit record.
func (s *Service) VerifyAll() []IntegrityProblem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyAllLocked()
}

func (s *Service) verifyAllLocked() []IntegrityProblem {
	var problems []IntegrityProblem
	var prevChain string
	var prevSeq int64
	for _, id := range s.store.versionOrder {
		v := s.store.versionsByID[id]
		if err := verifyVersionContent(v); err != nil {
			problems = append(problems, IntegrityProblem{Kind: "VERSION", ID: v.ID, Detail: err.Error()})
		}
		if v.PrevChainHash != prevChain {
			problems = append(problems, IntegrityProblem{Kind: "VERSION_LINK", ID: v.ID, Detail: "chain link broken"})
		}
		if v.Seq != prevSeq+1 {
			problems = append(problems, IntegrityProblem{Kind: "VERSION_SEQ", ID: v.ID, Detail: "sequence broken"})
		}
		prevChain = v.ChainHash
		prevSeq = v.Seq
	}
	var prevAuditHash string
	var prevAuditSeq int64
	for _, id := range s.store.auditOrder {
		a := s.store.auditsByID[id]
		if err := verifyAuditRecord(a); err != nil {
			problems = append(problems, IntegrityProblem{Kind: "AUDIT", ID: a.ID, Detail: err.Error()})
		}
		if a.PrevHash != prevAuditHash {
			problems = append(problems, IntegrityProblem{Kind: "AUDIT_LINK", ID: a.ID, Detail: "chain link broken"})
		}
		if a.Seq != prevAuditSeq+1 {
			problems = append(problems, IntegrityProblem{Kind: "AUDIT_SEQ", ID: a.ID, Detail: "sequence broken"})
		}
		prevAuditHash = a.RecordHash
		prevAuditSeq = a.Seq
	}
	var prevCorrHash string
	var prevCorrSeq int64
	for _, c := range s.store.listCorrections() {
		if err := verifyCorrection(c); err != nil {
			problems = append(problems, IntegrityProblem{Kind: "CORRECTION", ID: c.ID, Detail: err.Error()})
		}
		if c.PrevHash != prevCorrHash {
			problems = append(problems, IntegrityProblem{Kind: "CORRECTION_LINK", ID: c.ID, Detail: "chain link broken"})
		}
		if c.Seq != prevCorrSeq+1 {
			problems = append(problems, IntegrityProblem{Kind: "CORRECTION_SEQ", ID: c.ID, Detail: "sequence broken"})
		}
		prevCorrHash = c.RecordHash
		prevCorrSeq = c.Seq
	}
	return problems
}
