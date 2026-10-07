package compensate

import "sort"

// NaiveModel is an independent, deliberately simple sequential compensation
// model used as the oracle for randomized differential tests. It is written
// in the most literal way possible — plain maps, a stack of snapshots, no
// locks, no concurrency — and shares no implementation code with Engine
// beyond the input/output types.
type NaiveModel struct {
	objs     map[InstanceID]Attrs
	versions map[InstanceID]int64
	links    map[LinkID]Link
	polluted map[InstanceID]int
}

// NewNaiveModel builds a fresh naive model from a deep copy of g.
func NewNaiveModel(g *Graph) *NaiveModel {
	m := &NaiveModel{
		objs:     map[InstanceID]Attrs{},
		versions: map[InstanceID]int64{},
		links:    map[LinkID]Link{},
		polluted: map[InstanceID]int{},
	}
	g.lock()
	for id, o := range g.objs {
		cp := make(Attrs, len(o.Attrs))
		for k, v := range o.Attrs {
			cp[k] = v
		}
		m.objs[id] = cp
		m.versions[id] = o.Version
	}
	for id, l := range g.links {
		m.links[id] = *l
	}
	for id, idx := range g.polluted {
		m.polluted[id] = idx
	}
	g.unlock()
	return m
}

// NaiveOutcome is the oracle verdict for one action.
type NaiveOutcome struct {
	Committed    bool
	Primary      ReasonClass
	FailedOp     int
	CompFailures []CompFailure
	Polluted     map[InstanceID]int
	Attrs        map[InstanceID]Attrs
	Versions     map[InstanceID]int64
	Links        map[LinkID]bool
}

// cloneWorld captures the observable state plus contamination map.
func (m *NaiveModel) cloneWorld() (map[InstanceID]Attrs, map[InstanceID]int64, map[LinkID]bool, map[InstanceID]int) {
	attrs := make(map[InstanceID]Attrs, len(m.objs))
	for id, a := range m.objs {
		cp := make(Attrs, len(a))
		for k, v := range a {
			cp[k] = v
		}
		attrs[id] = cp
	}
	vers := make(map[InstanceID]int64, len(m.versions))
	for id, v := range m.versions {
		vers[id] = v
	}
	links := make(map[LinkID]bool, len(m.links))
	for id := range m.links {
		links[id] = true
	}
	pol := make(map[InstanceID]int, len(m.polluted))
	for id, idx := range m.polluted {
		pol[id] = idx
	}
	return attrs, vers, links, pol
}

type naiveUndo struct {
	opIndex int
	apply   func()
}

// Run executes one action sequentially. busy, when non-nil, reports which
// resource tokens are held by an external action, simulating contention.
func (m *NaiveModel) Run(a Action, busy map[string]bool) NaiveOutcome {
	out := NaiveOutcome{FailedOp: -1}

	involved := map[InstanceID]bool{}
	tokenSet := map[string]bool{}
	for _, op := range a.Ops {
		for _, id := range op.involvedInstances() {
			involved[id] = true
		}
		for _, t := range op.resources() {
			tokenSet[t] = true
		}
	}

	// 1) contamination preflight (highest fixed priority)
	var ids []InstanceID
	for id := range involved {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if idx, ok := m.polluted[id]; ok {
			out.Primary = ReasonContaminated
			_ = idx
			out.FailedOp = -1
			m.finish(&out)
			return out
		}
	}

	// 2) contention preflight (before any mutation)
	for t := range tokenSet {
		if busy[t] {
			out.Primary = ReasonContention
			m.finish(&out)
			return out
		}
	}

	// 3) literal forward loop; snapshot + undo are taken together
	var stack []naiveUndo
	failedAt := -1
	for i, op := range a.Ops {
		fp := FaultNone
		if a.Inject != nil && a.Inject.Apply != nil {
			fp = a.Inject.Apply[i]
		}
		if fp == FaultApplyPanic {
			failedAt = i
			break
		}
		if fp == FaultApplyFail {
			failedAt = i
			break
		}
		switch op.Kind {
		case OpSetAttrs:
			if _, ok := m.objs[op.Object]; !ok {
				failedAt = i
				break
			}
			old := make(Attrs, len(m.objs[op.Object]))
			for k, v := range m.objs[op.Object] {
				old[k] = v
			}
			oldVer := m.versions[op.Object]
			target := op.Object
			updates := op.Attrs
			idx := i
			undoFn := func() {
				m.objs[target] = old
				m.versions[target] = oldVer
			}
			if m.undoFaults(a, idx) {
				// nothing: fault surfaces during compensation only
			}
			for k, v := range updates {
				m.objs[op.Object][k] = v
			}
			m.versions[op.Object]++
			stack = append(stack, naiveUndo{opIndex: i, apply: undoFn})
		case OpCreateLink:
			if _, ok := m.links[op.Link.ID]; ok {
				failedAt = i
				break
			}
			l := op.Link
			idx := i
			m.links[l.ID] = l
			stack = append(stack, naiveUndo{opIndex: idx, apply: func() { delete(m.links, l.ID) }})
		case OpDeleteLink:
			old, ok := m.links[op.Link.ID]
			if !ok {
				failedAt = i
				break
			}
			idx := i
			delete(m.links, op.Link.ID)
			stack = append(stack, naiveUndo{opIndex: idx, apply: func() { m.links[old.ID] = old }})
		case OpValidate:
			if _, ok := m.objs[op.Object]; !ok {
				failedAt = i
				break
			}
			idx := i
			stack = append(stack, naiveUndo{opIndex: idx, apply: func() {}})
		}
		if failedAt >= 0 {
			break
		}
	}

	if failedAt < 0 {
		out.Committed = true
		m.finish(&out)
		return out
	}

	// 4) literal reverse loop; every inverse is attempted even after failure
	out.FailedOp = failedAt
	out.Primary = ReasonBusinessReject
	blocked := map[string]bool{}
	for j := len(stack) - 1; j >= 0; j-- {
		en := stack[j]
		op := a.Ops[en.opIndex]
		blockedHere := false
		if op.Kind != OpValidate {
			for _, t := range op.resources() {
				if blocked[t] {
					blockedHere = true
				}
			}
		}
		fp := FaultNone
		if a.Inject != nil && a.Inject.Undo != nil {
			fp = a.Inject.Undo[en.opIndex]
		}
		switch {
		case blockedHere:
			out.CompFailures = append(out.CompFailures, CompFailure{
				OpIndex: en.opIndex, Class: ReasonCompensationFailed,
				Detail: "skipped: resource held by earlier failed compensation"})
			for _, id := range op.involvedInstances() {
				m.mark(id, en.opIndex)
			}
		case fp == FaultUndoFail || fp == FaultUndoPanic:
			detail := "injected undo failure"
			if fp == FaultUndoPanic {
				detail = "undo panic: injected undo panic"
			}
			out.CompFailures = append(out.CompFailures, CompFailure{
				OpIndex: en.opIndex, Class: ReasonCompensationFailed, Detail: detail})
			for _, t := range op.resources() {
				blocked[t] = true
			}
			for _, id := range op.involvedInstances() {
				m.mark(id, en.opIndex)
			}
		default:
			en.apply()
		}
	}
	if len(out.CompFailures) > 0 {
		out.Primary = HigherReason(out.Primary, ReasonCompensationFailed)
	}
	m.finish(&out)
	return out
}

func (m *NaiveModel) undoFaults(a Action, idx int) bool {
	if a.Inject == nil || a.Inject.Undo == nil {
		return false
	}
	_, ok := a.Inject.Undo[idx]
	return ok
}

func (m *NaiveModel) mark(id InstanceID, idx int) {
	if cur, ok := m.polluted[id]; !ok || idx < cur {
		m.polluted[id] = idx
	}
}

func (m *NaiveModel) finish(out *NaiveOutcome) {
	out.Attrs, out.Versions, out.Links, out.Polluted = m.cloneWorld()
}

// Repair mirrors Engine.Repair for differential scenarios.
func (m *NaiveModel) Repair(id InstanceID) { delete(m.polluted, id) }
