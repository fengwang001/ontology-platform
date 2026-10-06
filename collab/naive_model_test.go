package collab

// naiveModel is an independent, deliberately simple reimplementation of the
// specification used for differential testing. It uses different data
// structures from the real server (plain maps keyed by seq, full group
// snapshots copied at group start) and follows the prose spec line by line.

type naiveField struct {
	kind    Kind
	present bool
	value   int64
	version int64
}

type naiveDoc struct {
	fields   map[string]*naiveField
	revision int64
}

type naiveClient struct {
	maxSeq  int64
	lastNow int64
	results map[int64]OpResult // full history (no ring): independent design
}

type naiveModel struct {
	docs    map[string]*naiveDoc
	clients map[[2]string]*naiveClient
}

func newNaive(schema map[string]Kind) *naiveModel {
	m := &naiveModel{
		docs:    map[string]*naiveDoc{},
		clients: map[[2]string]*naiveClient{},
	}
	d := &naiveDoc{fields: map[string]*naiveField{}}
	for name, kind := range schema {
		f := &naiveField{kind: kind}
		if kind == KindAdd {
			f.present = true
		}
		d.fields[name] = f
	}
	m.docs["d"] = d
	return m
}

func (m *naiveModel) client(k [2]string) *naiveClient {
	c := m.clients[k]
	if c == nil {
		c = &naiveClient{lastNow: -1, results: map[int64]OpResult{}}
		m.clients[k] = c
	}
	return c
}

// sync mirrors the spec: returns (results, rejectReason-string). A reject
// changes neither document nor clock.
func (m *naiveModel) sync(client string, ops []Op, now int64) ([]OpResult, string) {
	d := m.docs["d"]
	priorMax := m.clients[[2]string{client, "d"}]
	maxSeqBefore := int64(0)
	if priorMax != nil {
		maxSeqBefore = priorMax.maxSeq
	}

	// parameter checks
	if now < 0 || now > 1_000_000_000_000 {
		return nil, "invalid"
	}
	if len(ops) < 1 || len(ops) > 500 {
		return nil, "invalid"
	}
	for i, op := range ops {
		if op.Group == "" || op.Field == "" || (op.Type != OpSet && op.Type != OpAdd) {
			return nil, "invalid"
		}
		if i > 0 && op.Seq != ops[i-1].Seq+1 {
			return nil, "invalid"
		}
		if _, ok := d.fields[op.Field]; !ok {
			return nil, "invalid"
		}
		if op.Type == OpSet && (op.Value < MinInt || op.Value > MaxInt) {
			return nil, "invalid"
		}
		if op.Type == OpAdd && op.Delta == 0 {
			return nil, "invalid"
		}
	}
	if ops[0].Seq < 1 {
		return nil, "invalid"
	}
	type grp struct {
		start, end int
		depends    bool
	}
	var groups []grp
	for i := 0; i < len(ops); {
		j := i + 1
		for j < len(ops) && ops[j].Group == ops[i].Group {
			if ops[j].Depends != ops[i].Depends {
				return nil, "invalid"
			}
			j++
		}
		for k := j; k < len(ops); k++ {
			if ops[k].Group == ops[i].Group {
				return nil, "invalid"
			}
		}
		groups = append(groups, grp{i, j, ops[i].Depends})
		// duplicate set on a set-field within the group (new ops only)
		seen := map[string]bool{}
		for k := i; k < j; k++ {
			op := ops[k]
			if op.Seq <= maxSeqBefore {
				continue
			}
			if op.Type == OpSet && d.fields[op.Field].kind == KindSet {
				if seen[op.Field] {
					return nil, "invalid"
				}
				seen[op.Field] = true
			}
		}
		i = j
	}
	if len(groups) > 0 && groups[0].depends {
		return nil, "invalid"
	}

	c := m.client([2]string{client, "d"})

	if now < c.lastNow {
		return nil, "clock"
	}

	if c.maxSeq > 0 && ops[0].Seq <= c.maxSeq-RetainWindow {
		return nil, "expired"
	}

	newStart := 0
	for newStart < len(ops) && ops[newStart].Seq <= c.maxSeq {
		newStart++
	}
	if newStart < len(ops) && ops[newStart].Seq != c.maxSeq+1 {
		return nil, "gap"
	}

	results := make([]OpResult, len(ops))
	for i := 0; i < newStart; i++ {
		results[i] = c.results[ops[i].Seq]
	}

	prevOK := true
	for _, g := range groups {
		// snapshot at group start: full copy of all fields
		snap := map[string]*naiveField{}
		for name, f := range d.fields {
			cp := *f
			snap[name] = &cp
		}
		// only new ops participate in verdicts/commit
		hasNew := g.end > newStart
		if !hasNew {
			// Replay-only group: verbatim verdicts stand; dependency is not
			// re-evaluated because the group was historically resolved once.
			okG := true
			for k := g.start; k < g.end; k++ {
				r := results[k]
				if r.Kind != ResApplied && r.Kind != ResMerged {
					okG = false
				}
			}
			prevOK = okG
			continue
		}
		if g.depends && !prevOK {
			for k := g.start; k < g.end; k++ {
				if k >= newStart {
					results[k] = OpResult{Kind: ResDepFailed}
				}
			}
			prevOK = false
			continue
		}

		// evaluate new ops against snapshot independently
		type setC struct {
			field string
			value int64
		}
		var sets []setC
		adds := map[string]int64{}
		failKind := ResultKind(0)
		failAt := -1
		failPayload := OpResult{}
		for idx := g.start; idx < g.end; idx++ {
			if idx < newStart {
				continue
			}
			if failAt >= 0 {
				continue
			}
			op := ops[idx]
			f := snap[op.Field]
			switch {
			case (op.Type == OpSet && f.kind != KindSet) || (op.Type == OpAdd && f.kind != KindAdd):
				failKind, failAt = ResKindMismatch, idx
				failPayload = OpResult{Value: f.value, Version: f.version, Present: f.present}
			case op.Type == OpSet:
				switch {
				case f.version < op.BaseVer:
					failKind, failAt = ResAhead, idx
					failPayload = OpResult{Value: f.value, Version: f.version, Present: f.present}
				case f.version > op.BaseVer:
					if f.present && f.value == op.Value {
						// merged
					} else {
						failKind, failAt = ResConflict, idx
						failPayload = OpResult{Value: f.value, Version: f.version, Present: f.present}
					}
				default:
					sets = append(sets, setC{op.Field, op.Value})
				}
			default:
				if f.value+adds[op.Field]+op.Delta < MinInt ||
					f.value+adds[op.Field]+op.Delta > MaxInt {
					failKind, failAt = ResOverflow, idx
					failPayload = OpResult{Value: f.value, Version: f.version, Present: true}
				} else {
					adds[op.Field] += op.Delta
				}
			}
		}
		if failAt >= 0 {
			for idx := g.start; idx < g.end; idx++ {
				switch {
				case idx < newStart:
				case idx == failAt:
					results[idx] = failPayload
					results[idx].Kind = failKind
				default:
					results[idx] = OpResult{Kind: ResGroupAborted}
				}
			}
			prevOK = false
			continue
		}

		// success: classify and commit
		for idx := g.start; idx < g.end; idx++ {
			if idx < newStart {
				continue
			}
			op := ops[idx]
			f := snap[op.Field]
			if op.Type == OpSet && f.version > op.BaseVer {
				results[idx] = OpResult{Kind: ResMerged}
			} else {
				results[idx] = OpResult{Kind: ResApplied}
			}
		}
		effective := map[string]bool{}
		for _, sc := range sets {
			f := snap[sc.field]
			if !f.present || f.value != sc.value {
				effective[sc.field] = true
			}
		}
		for field, net := range adds {
			if net != 0 {
				effective[field] = true
			}
		}
		if len(effective) > 0 {
			d.revision++
			rev := d.revision
			for _, sc := range sets {
				if !effective[sc.field] {
					continue
				}
				f := d.fields[sc.field]
				f.value = sc.value
				f.present = true
				f.version = rev
			}
			for field, net := range adds {
				if !effective[field] {
					continue
				}
				f := d.fields[field]
				f.value = snap[field].value + net
				f.version = rev
			}
		}
		prevOK = true
	}

	// persist new ops + clock
	for i := newStart; i < len(ops); i++ {
		results[i].Seq = ops[i].Seq
		c.results[ops[i].Seq] = results[i]
	}
	if newStart < len(ops) {
		c.maxSeq = ops[len(ops)-1].Seq
	}
	c.lastNow = now
	return results, ""
}
