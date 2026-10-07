package importguard

import (
	"fmt"
	"sort"
)

// NaiveBatchImport is an independent, deliberately straightforward reference
// implementation of the same specification:
//
//   - batch rejects (illegal mode, then missing subject) before any work;
//   - records are processed ONE AT A TIME in index order;
//   - each record's state base is re-read live (so earlier same-batch writes
//     are visible), while its permission answers are answered from a
//     start-of-batch snapshot index (same semantics as the gatekeeper);
//   - every failure rolls the single record back and never touches others.
//
// The decision/commit logic is written independently from Gatekeeper (plain
// index-order iteration, no goroutines, no two-phase split, separate local
// helpers); it only reuses the immutable Store/Snapshot primitives so the
// randomized differential test compares two genuinely independent
// constructions of the same rules. It assumes the caller (test harness) has
// arranged mutual exclusion; it is a reference oracle, not production code.
func NaiveBatchImport(s *Store, subject string, mode Mode, entries []Entry) BatchResult {
	logger := nopLogger{}
	nlog := func(stage string, in map[string]any, out, basis string) {
		logger.LogDecision(DecisionRecord{Stage: stage, Inputs: in, Output: out, Basis: basis})
	}

	if mode != ModeAtomic && mode != ModeLenient {
		return BatchResult{Rejected: true, Failure: &Failure{
			Category: ErrBatchInvalidParameter,
			Message:  fmt.Sprintf("missing or illegal import mode %q", mode),
		}}
	}

	s.mu.Lock()
	snap := takeSnapshotLocked(s)
	if !snap.subjects[subject] {
		s.mu.Unlock()
		return BatchResult{Rejected: true, Failure: &Failure{
			Category: ErrSubjectNotFound,
			Message:  fmt.Sprintf("subject %q not found at batch start", subject),
		}}
	}

	res := make([]RecordResult, 0, len(entries))
	for i, e := range entries {
		rr := naiveOne(s, snap, subject, mode, i, e)
		res = append(res, rr)
		nlog("naive.record", map[string]any{"index": i, "object": e.ObjectID}, string(rr.Status), "sequential live-read decision")
	}
	s.mu.Unlock()
	return BatchResult{Records: res}
}

func naiveOne(s *Store, snap *Snapshot, subject string, mode Mode, i int, e Entry) RecordResult {
	failed := func(cat ErrorCategory, msg string, fields []string) RecordResult {
		return RecordResult{ObjectID: e.ObjectID, Index: i, Status: StatusFailed,
			Failure: &Failure{Category: cat, Message: msg, Fields: fields}}
	}

	otype, ok := snap.types[e.Type]
	if !ok {
		return failed(ErrObjectTypeNotFound, fmt.Sprintf("type %q missing", e.Type), nil)
	}
	if e.Semantic != SemanticCreate && e.Semantic != SemanticUpdate {
		return failed(ErrSemanticMismatch, fmt.Sprintf("bad semantic %q", e.Semantic), nil)
	}

	// Live-read the current object (visible earlier same-batch writes).
	cur, exists := s.getObjectLocked(e.ObjectID)
	if e.Semantic == SemanticUpdate && !exists {
		return failed(ErrSemanticMismatch, "update of absent object", nil)
	}
	if e.Semantic == SemanticCreate && exists {
		return failed(ErrSemanticMismatch, "create of present object", nil)
	}

	// Tentative change set; committed only if all later checks pass.
	change := map[string]PropertyValue{}
	var denied []string
	fields := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	for _, f := range fields {
		var probes int
		allow, _ := snap.canWrite(subject, e.Type, f, &probes)
		if allow {
			change[f] = e.Fields[f]
		} else {
			denied = append(denied, f)
		}
	}
	if len(denied) > 0 && mode == ModeAtomic {
		return failed(ErrFieldPermissionDenied, "atomic deny", denied)
	}

	// Required-property re-check against the tentative state.
	reqs := make([]string, 0)
	for r := range otype.Required {
		reqs = append(reqs, r)
	}
	sort.Strings(reqs)
	var missing []string
	for _, r := range reqs {
		if _, ok := change[r]; ok {
			continue
		}
		if e.Semantic == SemanticUpdate {
			if _, old := cur.Properties[r]; old {
				continue
			}
		}
		missing = append(missing, r)
	}
	if len(missing) > 0 {
		return failed(ErrRequiredPropertyMissing, "required missing after skip", missing)
	}

	// Commit (rollback is implicit: nothing was mutated before this point).
	if e.Semantic == SemanticCreate {
		s.insertObjectLocked(e.ObjectID, e.Type, change)
	} else {
		s.patchObjectLocked(e.ObjectID, change)
	}
	written := make([]string, 0, len(change))
	for k := range change {
		written = append(written, k)
	}
	sort.Strings(written)
	if len(denied) > 0 {
		return RecordResult{ObjectID: e.ObjectID, Index: i, Status: StatusPartial,
			Skipped: denied, WrittenKeys: written}
	}
	return RecordResult{ObjectID: e.ObjectID, Index: i, Status: StatusSuccess, WrittenKeys: written}
}
