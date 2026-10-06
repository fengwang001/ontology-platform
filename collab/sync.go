package collab

import "strconv"

// Logger observes Sync calls. Intended for tests and audit trails;
// implementations must be safe for concurrent use.
type Logger interface {
	LogBatch(client, docID string, ops []Op, now int64, results []OpResult, rejected error)
}

// SetLogger attaches an audit logger. Passing nil disables logging.
func (s *Server) SetLogger(l Logger) {
	s.mu.Lock()
	s.logger = l
	s.mu.Unlock()
}

// Sync submits one operation batch for (client, docID).
//
// Batch-level checks run in the required order; the first failure rejects
// the whole batch with no state or clock change:
// invalid parameter > clock rollback > replay expired > sequence gap.
func (s *Server) Sync(client, docID string, ops []Op, now int64) (res *BatchResult, err error) {
	s.mu.Lock()
	logger := s.logger
	s.mu.Unlock()
	if logger != nil {
		defer func() {
			var rs []OpResult
			if res != nil {
				rs = res.Results
			}
			logger.LogBatch(client, docID, ops, now, rs, err)
		}()
	}

	if client == "" || docID == "" {
		return nil, reject(ErrInvalidParam, "client/docId is empty")
	}
	if now < 0 || now > 1_000_000_000_000 {
		return nil, reject(ErrInvalidParam, "now out of range: "+strconv.FormatInt(now, 10))
	}
	if n := len(ops); n < 1 || n > MaxBatchOps {
		return nil, reject(ErrInvalidParam, "batch size must be in [1,"+
			strconv.Itoa(MaxBatchOps)+"], got "+strconv.Itoa(n))
	}

	// --- structural validation: seqs, groups, adjacency -----------------
	for i := range ops {
		op := &ops[i]
		if op.Group == "" {
			return nil, reject(ErrInvalidParam, "empty group id at seq "+strconv.FormatInt(op.Seq, 10))
		}
		if op.Type != OpSet && op.Type != OpAdd {
			return nil, reject(ErrInvalidParam, "unknown op type at seq "+strconv.FormatInt(op.Seq, 10))
		}
		if op.Field == "" {
			return nil, reject(ErrInvalidParam, "empty field at seq "+strconv.FormatInt(op.Seq, 10))
		}
		if i > 0 && op.Seq != ops[i-1].Seq+1 {
			return nil, reject(ErrInvalidParam,
				"seqs must be consecutive; got "+strconv.FormatInt(ops[i-1].Seq, 10)+
					" then "+strconv.FormatInt(op.Seq, 10))
		}
	}
	firstSeq := ops[0].Seq
	lastSeq := ops[len(ops)-1].Seq
	if firstSeq < 1 {
		return nil, reject(ErrInvalidParam, "seq starts below 1: "+strconv.FormatInt(firstSeq, 10))
	}

	// Group adjacency: equal group ids must be contiguous. The Depends flag
	// is a group-level declaration and must be identical inside the group.
	groupCount := 0
	for i := 0; i < len(ops); {
		j := i + 1
		for j < len(ops) && ops[j].Group == ops[i].Group {
			if ops[j].Depends != ops[i].Depends {
				return nil, reject(ErrInvalidParam,
					"inconsistent dependency flag inside group "+ops[i].Group)
			}
			j++
		}
		for k := j; k < len(ops); k++ {
			if ops[k].Group == ops[i].Group {
				return nil, reject(ErrInvalidParam, "group id not adjacent: "+ops[i].Group)
			}
		}
		if groupCount == 0 && ops[i].Depends {
			return nil, reject(ErrInvalidParam, "first group declares dependency: "+ops[i].Group)
		}
		groupCount++
		i = j
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.state.docs[docID]
	if !ok {
		return nil, reject(ErrUnknownDoc, docID)
	}

	// Schema/value checks need the document but remain parameter-level.
	for i := range ops {
		op := &ops[i]
		if _, ok := d.schema[op.Field]; !ok {
			return nil, reject(ErrInvalidParam,
				"field not in schema: "+op.Field+" at seq "+strconv.FormatInt(op.Seq, 10))
		}
		if op.Type == OpSet && (op.Value < MinInt || op.Value > MaxInt) {
			return nil, reject(ErrInvalidParam,
				"set value out of range at seq "+strconv.FormatInt(op.Seq, 10))
		}
		if op.Type == OpAdd && op.Delta == 0 {
			return nil, reject(ErrInvalidParam,
				"add delta must be non-zero at seq "+strconv.FormatInt(op.Seq, 10))
		}
	}
	cs := s.state.clients[[2]string{client, docID}]
	if cs == nil {
		cs = &clientState{lastNow: -1}
		s.state.clients[[2]string{client, docID}] = cs
	}

	// Duplicate Set on one overwrite field inside a group is illegal for new
	// ops only; replayed ops carry historical content and are served as-is.
	replayCount := 0
	for replayCount < len(ops) && ops[replayCount].Seq <= cs.maxSeq {
		replayCount++
	}
	for i := 0; i < len(ops); {
		j := i + 1
		for j < len(ops) && ops[j].Group == ops[i].Group {
			j++
		}
		seen := make(map[string]bool)
		from := i
		if from < replayCount {
			from = replayCount
		}
		for k := from; k < j; k++ {
			op := &ops[k]
			if op.Type == OpSet && d.schema[op.Field] == KindSet {
				if seen[op.Field] {
					return nil, reject(ErrInvalidParam,
						"duplicate Set on field "+op.Field+" in group "+op.Group)
				}
				seen[op.Field] = true
			}
		}
		i = j
	}

	// --- clock ----------------------------------------------------------
	if now < cs.lastNow {
		return nil, reject(ErrClockRollback,
			"now "+strconv.FormatInt(now, 10)+" < last "+strconv.FormatInt(cs.lastNow, 10))
	}

	// --- replay window --------------------------------------------------
	// Retained sequences are (maxSeq-RetainWindow, maxSeq]; the smallest
	// retained seq is maxSeq-RetainWindow+1.
	if cs.maxSeq > 0 && firstSeq <= cs.maxSeq-RetainWindow {
		return nil, reject(ErrReplayExpired,
			"seq "+strconv.FormatInt(firstSeq, 10)+" outside retain window ending at "+
				strconv.FormatInt(cs.maxSeq, 10))
	}

	// Split into a replayed prefix and a new suffix. Consecutive seq checks
	// plus the window check guarantee the prefix is contiguous and retained.
	newStart := 0
	for newStart < len(ops) && ops[newStart].Seq <= cs.maxSeq {
		newStart++
	}
	if newStart < len(ops) && ops[newStart].Seq != cs.maxSeq+1 {
		return nil, reject(ErrSeqGap,
			"new ops start at "+strconv.FormatInt(ops[newStart].Seq, 10)+
				", expected "+strconv.FormatInt(cs.maxSeq+1, 10))
	}

	results := make([]OpResult, len(ops))
	for i := 0; i < newStart; i++ {
		r, found := cs.ring.get(ops[i].Seq)
		if !found {
			return nil, reject(ErrReplayExpired, "seq "+strconv.FormatInt(ops[i].Seq, 10))
		}
		results[i] = r
	}

	if newStart < len(ops) {
		// --- execute groups ---------------------------------------------
		prevGroupSucceeded := true
		for i := 0; i < len(ops); {
			j := i + 1
			for j < len(ops) && ops[j].Group == ops[i].Group {
				j++
			}
			switch {
			case j <= newStart:
				// Replay-only group: success comes from verbatim verdicts.
				okGroup := true
				for k := i; k < j; k++ {
					kind := results[k].Kind
					if kind != ResApplied && kind != ResMerged {
						okGroup = false
					}
				}
				prevGroupSucceeded = okGroup
			case ops[i].Depends && !prevGroupSucceeded:
				for k := i; k < j; k++ {
					if k >= newStart {
						results[k] = OpResult{Kind: ResDepFailed}
					}
				}
				prevGroupSucceeded = false
			default:
				prevGroupSucceeded = executeGroup(d, ops, i, j, results, newStart)
			}
			i = j
		}

		// Persist new-op verdicts; every new op consumes its seq.
		for k := newStart; k < len(ops); k++ {
			results[k].Seq = ops[k].Seq
			cs.ring.put(results[k])
		}
		cs.maxSeq = lastSeq
	}

	cs.lastNow = now
	return &BatchResult{Results: results}, nil
}

type fieldSnap struct {
	kind    Kind
	value   int64
	version int64
	present bool
}

type commitChange struct {
	field string
	isSet bool  // true: overwrite with value; false: accumulator net delta
	value int64 // set value or net add delta
}

// executeGroup evaluates one transaction group against a snapshot taken at
// group start: every new op sees the same state, never earlier in-group ops.
// Add deltas to one field are summed against the snapshot and the first Add
// whose cumulative result leaves range fails the group with ResOverflow.
// On the first failing op all remaining new ops become ResGroupAborted.
// On full success the effective changes are committed to d with one revision
// bump iff at least one effective change exists.
func executeGroup(d *doc, ops []Op, gStart, gEnd int, results []OpResult, newStart int) bool {
	snap := make(map[string]fieldSnap)
	for i := gStart; i < gEnd; i++ {
		op := &ops[i]
		if _, ok := snap[op.Field]; !ok {
			f := d.fields[op.Field]
			snap[op.Field] = fieldSnap{
				kind: f.kind, value: f.value, version: f.version, present: f.present,
			}
		}
	}

	netAdd := make(map[string]int64)
	changes := make([]commitChange, 0, gEnd-gStart)

	// Evaluate every new op against the snapshot. Judgement stops at the
	// first failure; firstFail is its absolute index in results.
	firstFail := -1
	for idx := gStart; idx < gEnd; idx++ {
		if idx < newStart {
			continue // replayed op keeps its verbatim result
		}
		if firstFail >= 0 {
			continue
		}
		op := &ops[idx]
		f := snap[op.Field]
		switch {
		case (op.Type == OpSet && f.kind != KindSet) || (op.Type == OpAdd && f.kind != KindAdd):
			results[idx] = failResult(ResKindMismatch, f)
			firstFail = idx
		case op.Type == OpSet:
			switch {
			case f.version < op.BaseVer:
				results[idx] = failResult(ResAhead, f)
				firstFail = idx
			case f.version > op.BaseVer:
				if f.present && f.value == op.Value {
					results[idx] = OpResult{Kind: ResMerged}
				} else {
					results[idx] = failResult(ResConflict, f)
					firstFail = idx
				}
			default:
				results[idx] = OpResult{Kind: ResApplied}
				changes = append(changes, commitChange{field: op.Field, isSet: true, value: op.Value})
			}
		default: // Add on KindAdd
			proposed := f.value + netAdd[op.Field] + op.Delta
			if proposed < MinInt || proposed > MaxInt {
				results[idx] = failResult(ResOverflow, f)
				firstFail = idx
			} else {
				netAdd[op.Field] += op.Delta
				results[idx] = OpResult{Kind: ResApplied}
			}
		}
	}

	if firstFail >= 0 {
		for idx := gStart; idx < gEnd; idx++ {
			if idx < newStart {
				continue
			}
			if idx != firstFail {
				results[idx] = OpResult{Kind: ResGroupAborted}
			}
		}
		return false
	}

	effectiveFields := make(map[string]bool)
	for idx := gStart; idx < gEnd; idx++ {
		if idx < newStart || results[idx].Kind != ResApplied {
			continue
		}
		if ops[idx].Type == OpSet {
			f := snap[ops[idx].Field]
			if !f.present || f.value != ops[idx].Value {
				effectiveFields[ops[idx].Field] = true
			}
		}
	}
	for field, net := range netAdd {
		if net != 0 {
			effectiveFields[field] = true
			changes = append(changes, commitChange{field: field, isSet: false, value: net})
		}
	}

	if len(effectiveFields) > 0 {
		d.revision++
		rev := d.revision
		for _, c := range changes {
			if !effectiveFields[c.field] {
				continue
			}
			f := d.fields[c.field]
			if c.isSet {
				f.value = c.value
				f.present = true
			} else {
				f.value = snap[c.field].value + c.value
			}
			f.version = rev
		}
	}
	return true
}

func failResult(kind ResultKind, f fieldSnap) OpResult {
	return OpResult{
		Kind:    kind,
		Value:   f.value,
		Version: f.version,
		Present: f.present,
	}
}
