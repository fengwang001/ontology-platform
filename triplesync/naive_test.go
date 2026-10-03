package triplesync

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// naivePlan is a from-scratch implementation of the spec used as an oracle in
// differential tests. Every decision appends a human-readable rationale to log.
func naivePlan(base, local, remote Snapshot) (Plan, []string, error) {
	var log []string
	add := func(format string, args ...interface{}) {
		log = append(log, fmt.Sprintf(format, args...))
	}

	if !validateSnapshot(base) {
		add("base invalid")
		return Plan{}, log, ErrBadBase
	}
	if !validateSnapshot(local) {
		add("local invalid")
		return Plan{}, log, ErrBadLocal
	}
	if !validateSnapshot(remote) {
		add("remote invalid")
		return Plan{}, log, ErrBadRemote
	}
	if err := dirAgreement(base, local, remote); err != nil {
		add("dir flag disagreement: %v", err)
		return Plan{}, log, err
	}

	ids := unionIDs(base, local, remote)
	var toLocal, toRemote []Action
	var conflicts []Conflict
	type cand struct {
		id    int64
		x     int
		depth int
	}
	var cands []cand

	locEq := func(e1, e2 Entry) bool { return e1.Parent == e2.Parent && e1.Name == e2.Name }

	for _, id := range ids {
		be, bok := base[id]
		le, lok := local[id]
		re, rok := remote[id]
		switch {
		case !bok:
			switch {
			case lok && rok:
				if !locEq(le, re) {
					conflicts = append(conflicts, Conflict{id, ConflictLoc})
					add("id=%d created both sides, loc differs -> Loc conflict", id)
				} else {
					add("id=%d created both sides, same loc", id)
				}
				if !le.Dir && le.Hash != re.Hash {
					conflicts = append(conflicts, Conflict{id, ConflictContent})
					add("id=%d created both sides, hash differs -> Content conflict", id)
				}
			case lok:
				toRemote = append(toRemote, createAction(id, le))
				add("id=%d created local only -> Create to remote", id)
			case rok:
				toLocal = append(toLocal, createAction(id, re))
				add("id=%d created remote only -> Create to local", id)
			}
		case bok:
			switch {
			case !lok && !rok:
				add("id=%d deleted both sides -> no action", id)
			case !lok:
				changed := !locEq(re, be) || (!be.Dir && re.Hash != be.Hash)
				if changed {
					conflicts = append(conflicts, Conflict{id, ConflictDeleteModify})
					add("id=%d local deleted, remote modified -> DeleteModify", id)
				} else {
					toRemote = append(toRemote, Action{Kind: ActionDelete, ID: id})
					add("id=%d local deleted, remote unchanged -> Delete to remote", id)
					if be.Dir {
						cands = append(cands, cand{id, 0, depthOf(remote, id)})
					}
				}
			case !rok:
				changed := !locEq(le, be) || (!be.Dir && le.Hash != be.Hash)
				if changed {
					conflicts = append(conflicts, Conflict{id, ConflictDeleteModify})
					add("id=%d remote deleted, local modified -> DeleteModify", id)
				} else {
					toLocal = append(toLocal, Action{Kind: ActionDelete, ID: id})
					add("id=%d remote deleted, local unchanged -> Delete to local", id)
					if be.Dir {
						cands = append(cands, cand{id, 1, depthOf(local, id)})
					}
				}
			default:
				lLoc := !locEq(le, be)
				rLoc := !locEq(re, be)
				if lLoc && rLoc {
					if !locEq(le, re) {
						conflicts = append(conflicts, Conflict{id, ConflictLoc})
						add("id=%d loc changed both sides differently -> Loc conflict", id)
					} else {
						add("id=%d loc changed both sides identically -> none", id)
					}
				} else if lLoc {
					toRemote = append(toRemote, Action{Kind: ActionSetLoc, ID: id, Parent: le.Parent, Name: le.Name})
					add("id=%d loc changed local only -> SetLoc to remote", id)
				} else if rLoc {
					toLocal = append(toLocal, Action{Kind: ActionSetLoc, ID: id, Parent: re.Parent, Name: re.Name})
					add("id=%d loc changed remote only -> SetLoc to local", id)
				}
				if !be.Dir {
					lHash := le.Hash != be.Hash
					rHash := re.Hash != be.Hash
					if lHash && rHash {
						if le.Hash != re.Hash {
							conflicts = append(conflicts, Conflict{id, ConflictContent})
							add("id=%d hash changed both sides differently -> Content conflict", id)
						} else {
							add("id=%d hash changed both sides identically -> none", id)
						}
					} else if lHash {
						toRemote = append(toRemote, Action{Kind: ActionSetHash, ID: id, Hash: le.Hash})
						add("id=%d hash changed local only -> SetHash to remote", id)
					} else if rHash {
						toLocal = append(toLocal, Action{Kind: ActionSetHash, ID: id, Hash: re.Hash})
						add("id=%d hash changed remote only -> SetHash to local", id)
					}
				}
			}
		}
	}

	// Veto phase, deepest first.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].depth != cands[j].depth {
			return cands[i].depth > cands[j].depth
		}
		return cands[i].id < cands[j].id
	})
	candSet := make(map[int64]bool)
	for _, c := range cands {
		candSet[c.id] = true
	}
	stripCandDeletes := func(acts []Action) []Action {
		out := []Action{}
		for _, a := range acts {
			if a.Kind == ActionDelete && candSet[a.ID] {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	aliveL := applyActions(local, stripCandDeletes(toLocal))
	aliveR := applyActions(remote, stripCandDeletes(toRemote))
	alive := [2]map[int64]Entry{aliveL, aliveR}
	vetoed := map[int64]bool{}
	for _, c := range cands {
		y := 1 - c.x
		child := false
		for _, e := range alive[y] {
			if e.Parent == c.id {
				child = true
				break
			}
		}
		if !child {
			delete(alive[0], c.id)
			delete(alive[1], c.id)
			add("dir %d single-sided delete allowed (no surviving child)", c.id)
			continue
		}
		vetoed[c.id] = true
		alive[c.x][c.id] = alive[y][c.id]
		act := createAction(c.id, alive[y][c.id])
		if c.x == 0 {
			toLocal = append(toLocal, act)
		} else {
			toRemote = append(toRemote, act)
		}
		add("dir %d delete vetoed (surviving child): revive on side %d at (%d,%q)", c.id, c.x, act.Parent, act.Name)
	}
	dropVetoed := func(acts []Action) []Action {
		out := []Action{}
		for _, a := range acts {
			if a.Kind == ActionDelete && vetoed[a.ID] {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	toLocal = dropVetoed(toLocal)
	toRemote = dropVetoed(toRemote)

	// Simulate + name collision resolution, one side at a time.
	resolve := func(s Snapshot) {
		for {
			type k struct {
				p int64
				n string
			}
			groups := map[k][]int64{}
			for id, e := range s {
				key := k{e.Parent, e.Name}
				groups[key] = append(groups[key], id)
			}
			var dup k
			found := false
			var keys []k
			for key, m := range groups {
				if len(m) > 1 {
					keys = append(keys, key)
				}
			}
			if len(keys) == 0 {
				break
			}
			sort.Slice(keys, func(i, j int) bool {
				if keys[i].p != keys[j].p {
					return keys[i].p < keys[j].p
				}
				return keys[i].n < keys[j].n
			})
			dup = keys[0]
			found = true
			_ = found
			members := append([]int64(nil), groups[dup]...)
			sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
			for _, id := range members[1:] {
				e := s[id]
				name := e.Name + ".c" + strconv.FormatInt(id, 10)
				for {
					coll := false
					for oid, oe := range s {
						if oid != id && oe.Parent == e.Parent && oe.Name == name {
							coll = true
							break
						}
					}
					if !coll {
						break
					}
					name += "x"
				}
				e.Name = name
				s[id] = e
				add("rename id=%d to %q under parent %d", id, name, e.Parent)
			}
		}
	}
	simL := applyActions(local, toLocal)
	simR := applyActions(remote, toRemote)
	resolve(simL)
	resolve(simR)

	// Rebuild action lists from the simulated states: any id whose final state
	// differs from the raw action gets folded or a new SetLoc appended.
	remake := func(origin Snapshot, acts []Action, final map[int64]Entry) []Action {
		out := []Action{}
		for _, a := range acts {
			if fe, ok := final[a.ID]; ok && (a.Kind == ActionCreate || a.Kind == ActionSetLoc) {
				a.Parent = fe.Parent
				a.Name = fe.Name
			}
			if a.Kind == ActionDelete {
				if _, ok := final[a.ID]; ok {
					continue
				}
			}
			out = append(out, a)
		}
		// ids present in final but absent from any action touching loc:
		touched := map[int64]bool{}
		for _, a := range out {
			if a.Kind == ActionCreate || a.Kind == ActionSetLoc {
				touched[a.ID] = true
			}
		}
		var extra []int64
		for id := range final {
			oe, inOrigin := origin[id]
			if inOrigin && (oe.Parent != final[id].Parent || oe.Name != final[id].Name) && !touched[id] {
				extra = append(extra, id)
			}
		}
		sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
		for _, id := range extra {
			out = append(out, Action{Kind: ActionSetLoc, ID: id, Parent: final[id].Parent, Name: final[id].Name})
		}
		return out
	}
	toLocal = remake(local, toLocal, simL)
	toRemote = remake(remote, toRemote, simR)

	finalL := applyActions(local, toLocal)
	finalR := applyActions(remote, toRemote)
	if !validMerge(finalL) || !validMerge(finalR) {
		add("merged state invalid (cycle/dangling/dup)")
		return Plan{}, log, ErrMergeInvalid
	}

	toLocal = orderActions(toLocal, local, finalL)
	toRemote = orderActions(toRemote, remote, finalR)
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].ID != conflicts[j].ID {
			return conflicts[i].ID < conflicts[j].ID
		}
		return conflicts[i].Kind < conflicts[j].Kind
	})

	add("final: ToLocal=%v ToRemote=%v Conflicts=%v",
		actionSummary(toLocal), actionSummary(toRemote), conflicts)
	return Plan{nonNilActions(toLocal), nonNilActions(toRemote), nonNilConflicts(conflicts)}, log, nil
}

func actionSummary(acts []Action) string {
	var b strings.Builder
	for i, a := range acts {
		if i > 0 {
			b.WriteString(",")
		}
		name := map[ActionKind]string{ActionCreate: "C", ActionSetLoc: "L", ActionSetHash: "H", ActionDelete: "D"}[a.Kind]
		fmt.Fprintf(&b, "%s%d", name, a.ID)
		if a.Kind == ActionCreate || a.Kind == ActionSetLoc {
			fmt.Fprintf(&b, "@%d/%q", a.Parent, a.Name)
		}
	}
	return b.String()
}
