package refupdate

// adjItem is the per-instruction adjudication state.
type adjItem struct {
	instr  Instruction
	reason RejectReason
	before CommitID
	after  CommitID
	kind   RefKind
	pass   bool
}

// Push atomically adjudicates and applies one batch of instructions.
//
// The whole evaluation and the commit run under Store's single mutex, so the
// observed refs, graph and protection rules form one consistent snapshot and
// the batch is linearized at one instant against every other push and reader.
func (s *Store) Push(user string, instructions []Instruction) PushReport {
	s.mu.Lock()
	defer s.mu.Unlock()

	report := PushReport{Items: make([]ItemResult, len(instructions))}
	items := make([]adjItem, len(instructions))

	for i, in := range instructions {
		it := &items[i]
		it.instr = in
		it.before = s.refs[in.Ref]
		it.after = it.before
		report.Items[i] = ItemResult{Index: i, Before: it.before, After: it.before}

		create := in.OldID == ""
		deleteOp := in.NewID == ""
		if create && deleteOp {
			it.reason = RejectMalformedInstruction
			continue
		}

		if in.Ref == "" {
			// An empty reference name is an illegal name, not a namespace
			// error; the precedence table places invalid-name after the
			// both-values-empty parameter check only.
			it.reason = RejectInvalidRefName
			continue
		}
		it.kind = in.Ref.Kind()
		if it.kind == KindUnknown {
			it.reason = RejectMalformedInstruction
			continue
		}
		if !in.Ref.ValidSyntax() {
			it.reason = RejectInvalidRefName
			continue
		}

		if !deleteOp && !s.graph.Exists(in.NewID) {
			it.reason = RejectObjectMissing
			continue
		}

		if in.OldID != it.before {
			it.reason = RejectOldValueMismatch
			continue
		}

		rules := s.rules.Match(in.Ref)

		switch {
		case create:
			if rules.noCreate {
				it.reason = RejectProtectedCreate
				continue
			}
			if !rules.allowsUser(user) {
				it.reason = RejectProtectedAllowlist
				continue
			}
		case deleteOp:
			if rules.noDelete {
				it.reason = RejectProtectedDelete
				continue
			}
			if !rules.allowsUser(user) {
				it.reason = RejectProtectedAllowlist
				continue
			}
		default:
			if it.kind == KindTag {
				it.reason = RejectTagImmutable
				continue
			}
			fastForward := s.graph.IsDescendant(in.NewID, in.OldID)
			if !fastForward {
				if rules.noNonFF {
					it.reason = RejectProtectedNonFF
					continue
				}
				if !in.Force {
					it.reason = RejectNonFFWithoutForce
					continue
				}
			}
			if !rules.allowsUser(user) {
				it.reason = RejectProtectedAllowlist
				continue
			}
		}

		if deleteOp {
			it.after = ""
		} else {
			it.after = in.NewID
		}
		it.pass = true
	}

	batchReason := s.batchConflict(items)

	anyReject := batchReason != RejectNone
	for i := range items {
		if !items[i].pass {
			anyReject = true
			break
		}
	}

	if anyReject {
		for i := range items {
			it := &items[i]
			if !it.pass {
				report.Items[i].Reason = it.reason
				continue
			}
			if batchReason != RejectNone {
				report.Items[i].Reason = batchReason
			} else {
				report.Items[i].Reason = SkippedDueToOtherReject
			}
		}
		return report
	}

	for i := range items {
		it := &items[i]
		if it.after == "" {
			delete(s.refs, it.instr.Ref)
		} else {
			s.refs[it.instr.Ref] = it.after
		}
	}
	s.nextSeq++
	entry := AuditEntry{Seq: s.nextSeq, User: user, Items: make([]AuditItem, len(items))}
	for i := range items {
		it := &items[i]
		entry.Items[i] = AuditItem{Ref: it.instr.Ref, Before: it.before, After: it.after, Force: it.instr.Force}
		report.Items[i] = ItemResult{
			Index:    i,
			Reason:   RejectNone,
			Executed: true,
			Before:   it.before,
			After:    it.after,
		}
	}
	s.audit = append(s.audit, entry)
	report.Accepted = true
	report.AuditSeq = entry.Seq
	return report
}

// batchConflict checks batch-wide invariants among individually-passing
// instructions:
//   - same reference appearing more than once (covers tag delete+recreate);
//   - tag delete and recreate in one batch (explicit tag rule);
//   - ancestor-segment-prefix clashes measured against the final state, i.e.
//     created names versus surviving existing refs and other created names.
func (s *Store) batchConflict(items []adjItem) RejectReason {
	seen := make(map[RefName]struct{})
	deleted := make(map[RefName]struct{})
	tagDeleted := make(map[RefName]bool)
	tagCreated := make(map[RefName]bool)
	var created []RefName

	for _, it := range items {
		if !it.pass {
			continue
		}
		if _, dup := seen[it.instr.Ref]; dup {
			return RejectBatchConflict
		}
		seen[it.instr.Ref] = struct{}{}

		switch {
		case it.instr.OldID == "":
			created = append(created, it.instr.Ref)
			if it.kind == KindTag {
				tagCreated[it.instr.Ref] = true
			}
		case it.instr.NewID == "":
			deleted[it.instr.Ref] = struct{}{}
			if it.kind == KindTag {
				tagDeleted[it.instr.Ref] = true
			}
		}
	}
	for ref := range tagDeleted {
		if tagCreated[ref] {
			return RejectBatchConflict
		}
	}

	for _, name := range created {
		for existing := range s.refs {
			if _, isDeleted := deleted[existing]; isDeleted {
				continue
			}
			if RefPrefixConflict(name, existing) {
				return RejectBatchConflict
			}
		}
	}
	for i := 0; i < len(created); i++ {
		for j := i + 1; j < len(created); j++ {
			if RefPrefixConflict(created[i], created[j]) {
				return RejectBatchConflict
			}
		}
	}
	return RejectNone
}
