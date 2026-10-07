package ontology

import (
	"fmt"
	"sort"
)

// decideAndCommitLocked runs stages 2-4 and the state switch. The caller must
// hold commitMu and p.mu. All inspected records come from the batch's
// connected component in the link graph, so decision cost is O(batch size).
func (p *Platform) decideAndCommitLocked(b Batch, opIndex map[InstanceID]int) BatchResult {
	res := BatchResult{BatchID: b.ID}

	// Relevant instances: written + link endpoints + connected-component
	// closure over existing links. Two batches that could become linked are in
	// one component and are therefore serialized together via commitMu.
	relevant := make(map[InstanceID]struct{}, len(b.Ops)+2*len(b.Links))
	for _, op := range b.Ops {
		relevant[op.Instance] = struct{}{}
	}
	for _, lop := range b.Links {
		relevant[lop.A] = struct{}{}
		relevant[lop.B] = struct{}{}
	}
	frontier := make([]InstanceID, 0, len(relevant))
	for id := range relevant {
		frontier = append(frontier, id)
	}
	for len(frontier) > 0 {
		id := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for k := range p.adj[id] {
			peer := k.a
			if peer == id {
				peer = k.b
			}
			if _, have := relevant[peer]; !have {
				relevant[peer] = struct{}{}
				frontier = append(frontier, peer)
			}
		}
	}
	ordered := make([]InstanceID, 0, len(relevant))
	for id := range relevant {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	cur := make(map[InstanceID]*instance, len(ordered))
	baseStates := make(map[string]State, len(ordered))
	for _, id := range ordered {
		p.stats.InstanceInspections++
		if ins, ok := p.instances[id]; ok {
			cur[id] = ins
			baseStates[string(id)] = State{
				Type: string(ins.typ), Version: ins.version, Exists: true, Props: propsToJSON(ins.props),
			}
		} else {
			baseStates[string(id)] = State{Version: 0, Exists: false}
		}
	}
	incident := make(map[linkKey]struct{})
	for _, id := range ordered {
		for k := range p.adj[id] {
			incident[k] = struct{}{}
		}
	}
	touchedLinkTypes := make(map[TypeName]struct{}, len(b.Links))
	for _, lop := range b.Links {
		touchedLinkTypes[lop.Link] = struct{}{}
	}

	failNow := func(class FailureClass, detail string) BatchResult {
		return p.failLocked(b, res, class, detail, baseStates)
	}

	// Stage 2: per-instance base versions. Nonexistent instance = version 0.
	for _, op := range b.Ops {
		var have int64
		if ins, ok := cur[op.Instance]; ok {
			have = ins.version
		}
		if have != op.BaseVersion {
			return failNow(FailureVersionConflict,
				fmt.Sprintf("instance %s base %d != current %d", op.Instance, op.BaseVersion, have))
		}
	}

	// Stage 3: validation hooks, one per op, on deep-copied proposed state.
	hookNotes := make([]string, 0, len(b.Ops))
	for _, op := range b.Ops {
		ot, ok := p.objectTypes[op.Type]
		if !ok {
			return failNow(FailureHookRejected,
				fmt.Sprintf("instance %s: object type %s not registered", op.Instance, op.Type))
		}
		if old := cur[op.Instance]; old != nil && old.typ != op.Type {
			return failNow(FailureHookRejected,
				fmt.Sprintf("instance %s: cannot change type from %s to %s", op.Instance, old.typ, op.Type))
		}
		var currentProps, proposedProps Properties
		if old := cur[op.Instance]; old != nil {
			currentProps = cloneProps(old.props)
		}
		if op.Props != nil {
			proposedProps = cloneProps(op.Props)
		}
		if ot.Hook != nil {
			p.stats.HooksRun++
			if err := ot.Hook(currentProps, proposedProps); err != nil {
				return failNow(FailureHookRejected,
					fmt.Sprintf("instance %s hook: %v", op.Instance, err))
			}
			hookNotes = append(hookNotes, fmt.Sprintf("%s:ok", op.Instance))
		}
	}

	// Stage 4: cardinality on the final link set.
	finalLinks := make(map[linkKey]struct{}, len(incident)+len(b.Links))
	for k := range incident {
		finalLinks[k] = struct{}{}
	}
	for _, lop := range b.Links {
		k := normalizeLink(lop.Link, lop.A, lop.B)
		if lop.Add {
			finalLinks[k] = struct{}{}
		} else {
			delete(finalLinks, k)
		}
	}
	// Deleting an instance invalidates every link still incident to it.
	for _, op := range b.Ops {
		if op.Props != nil {
			continue
		}
		for k := range finalLinks {
			if k.a == op.Instance || k.b == op.Instance {
				touchedLinkTypes[k.link] = struct{}{}
			}
		}
	}

	eff := func(id InstanceID) (*instance, bool) {
		if idx, touched := opIndex[id]; touched {
			op := b.Ops[idx]
			if op.Props == nil {
				return nil, false
			}
			return &instance{id: id, typ: op.Type}, true
		}
		ins, ok := cur[id]
		return ins, ok
	}

	type roleCount struct{ a, b int }
	deg := make(map[InstanceID]map[TypeName]*roleCount)
	addDeg := func(id InstanceID, lt TypeName, side string) {
		m, ok := deg[id]
		if !ok {
			m = map[TypeName]*roleCount{}
			deg[id] = m
		}
		c := m[lt]
		if c == nil {
			c = &roleCount{}
			m[lt] = c
		}
		if side == "a" {
			c.a++
		} else {
			c.b++
		}
	}

	checkIDs := make(map[InstanceID]struct{})
	for k := range finalLinks {
		if _, touched := touchedLinkTypes[k.link]; !touched {
			continue
		}
		lt, ok := p.linkTypes[k.link]
		if !ok {
			return failNow(FailureCardinality,
				fmt.Sprintf("link type %s not registered", k.link))
		}
		ea, aOK := eff(k.a)
		eb, bOK := eff(k.b)
		if !aOK || !bOK {
			return failNow(FailureCardinality,
				fmt.Sprintf("link %s(%s,%s) references missing instance", k.link, k.a, k.b))
		}
		matchAB := ea.typ == lt.LeftType && eb.typ == lt.RightType
		matchBA := ea.typ == lt.RightType && eb.typ == lt.LeftType
		if !matchAB && !matchBA {
			return failNow(FailureCardinality,
				fmt.Sprintf("link %s(%s,%s) endpoint types %s,%s violate schema %s/%s",
					k.link, k.a, k.b, ea.typ, eb.typ, lt.LeftType, lt.RightType))
		}
		checkIDs[k.a] = struct{}{}
		checkIDs[k.b] = struct{}{}
		if lt.LeftType == lt.RightType {
			addDeg(k.a, k.link, "a")
			addDeg(k.b, k.link, "a")
		} else if matchAB {
			addDeg(k.a, k.link, "a")
			addDeg(k.b, k.link, "b")
		} else {
			addDeg(k.a, k.link, "b")
			addDeg(k.b, k.link, "a")
		}
	}
	for _, lop := range b.Links {
		checkIDs[lop.A] = struct{}{}
		checkIDs[lop.B] = struct{}{}
	}
	for id := range checkIDs {
		ins, exists := eff(id)
		for ltName := range touchedLinkTypes {
			lt, ok := p.linkTypes[ltName]
			if !ok {
				continue
			}
			var n int
			var bound Cardinality
			switch {
			case lt.LeftType == lt.RightType:
				if !exists || ins.typ != lt.LeftType {
					continue
				}
				if c := deg[id][ltName]; c != nil {
					n = c.a
				}
				bound = lt.CardA
			case exists && ins.typ == lt.LeftType:
				if c := deg[id][ltName]; c != nil {
					n = c.a
				}
				bound = lt.CardA
			case exists && ins.typ == lt.RightType:
				if c := deg[id][ltName]; c != nil {
					n = c.b
				}
				bound = lt.CardB
			default:
				continue
			}
			if !within(n, bound) {
				return failNow(FailureCardinality,
					fmt.Sprintf("instance %s has %d links of type %s, require %s", id, n, ltName, bound))
			}
		}
	}

	// All checks passed: single state switch while p.mu is held.
	newVersions := make(map[InstanceID]int64, len(b.Ops))
	newState := make(map[string]State, len(b.Ops))
	for _, op := range b.Ops {
		if op.Props == nil {
			delete(p.instances, op.Instance)
			newState[string(op.Instance)] = State{Version: 0, Exists: false}
			newVersions[op.Instance] = 0
			continue
		}
		var v int64 = 1
		if old := p.instances[op.Instance]; old != nil {
			v = old.version + 1
		}
		p.instances[op.Instance] = &instance{id: op.Instance, typ: op.Type, version: v, props: cloneProps(op.Props)}
		newVersions[op.Instance] = v
		newState[string(op.Instance)] = State{
			Type: string(op.Type), Version: v, Exists: true, Props: propsToJSON(op.Props),
		}
	}
	for _, lop := range b.Links {
		k := normalizeLink(lop.Link, lop.A, lop.B)
		if lop.Add {
			if _, ok := p.links[k]; ok {
				continue
			}
			p.links[k] = struct{}{}
			p.addAdj(lop.A, k)
			p.addAdj(lop.B, k)
		} else {
			if _, ok := p.links[k]; !ok {
				continue
			}
			delete(p.links, k)
			p.delAdj(lop.A, k)
			p.delAdj(lop.B, k)
		}
	}
	p.tick++
	p.stats.Committed++
	tick := p.tick
	newLinks := finalLinkRecords(finalLinks, touchedLinkTypes)
	p.recordJournal(JournalEntry{
		BatchID: b.ID, Batch: recordBatch(b), Base: baseStates, OK: true,
		Tick: tick, HookNotes: hookNotes, NewState: newState, NewLinks: newLinks,
	})

	res.OK = true
	res.NewVersions = newVersions
	res.CommitTick = tick
	return res
}
