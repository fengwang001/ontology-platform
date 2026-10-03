package triplesync

import (
	"sort"
)

type sideState struct {
	present bool
	entry   Entry
}

type vetoCandidate struct {
	id    int64
	x     int // side that deleted: 0 = local, 1 = remote
	depth int
}

// NewPlanner constructs a planner from a legal base snapshot.
func NewPlanner(base Snapshot) (*Planner, error) {
	if !validateSnapshot(base) {
		return nil, ErrBadBase
	}
	cp := make(Snapshot, len(base))
	for id, e := range base {
		cp[id] = e
	}
	return &Planner{base: cp}, nil
}

// Plan compares local and remote against the base snapshot.
func (p *Planner) Plan(local, remote Snapshot) (Plan, error) {
	if !validateSnapshot(local) {
		return Plan{}, ErrBadLocal
	}
	if !validateSnapshot(remote) {
		return Plan{}, ErrBadRemote
	}
	if err := dirAgreement(p.base, local, remote); err != nil {
		return Plan{}, err
	}

	baseCopy := make(Snapshot, len(p.base))
	for id, e := range p.base {
		baseCopy[id] = e
	}
	ids := unionIDs(baseCopy, local, remote)

	var toLocal, toRemote []Action
	var conflicts []Conflict
	var vetoes []vetoCandidate

	get := func(s Snapshot, id int64) sideState {
		e, ok := s[id]
		return sideState{present: ok, entry: e}
	}

	for _, id := range ids {
		b := get(baseCopy, id)
		l := get(local, id)
		r := get(remote, id)

		switch {
		case !b.present:
			switch {
			case l.present && r.present:
				if l.entry.Parent != r.entry.Parent || l.entry.Name != r.entry.Name {
					conflicts = append(conflicts, Conflict{id, ConflictLoc})
				}
				if !l.entry.Dir && l.entry.Hash != r.entry.Hash {
					conflicts = append(conflicts, Conflict{id, ConflictContent})
				}
			case l.present:
				toRemote = append(toRemote, createAction(id, l.entry))
			case r.present:
				toLocal = append(toLocal, createAction(id, r.entry))
			}
		case b.present:
			switch {
			case !l.present && !r.present:
				// Gone on both sides: nothing to do.
			case !l.present:
				if r.entry.Parent != b.entry.Parent || r.entry.Name != b.entry.Name ||
					(!b.entry.Dir && r.entry.Hash != b.entry.Hash) {
					conflicts = append(conflicts, Conflict{id, ConflictDeleteModify})
				} else {
					toRemote = append(toRemote, Action{Kind: ActionDelete, ID: id})
					if b.entry.Dir {
						vetoes = append(vetoes, vetoCandidate{id: id, x: 0, depth: depthOf(remote, id)})
					}
				}
			case !r.present:
				if l.entry.Parent != b.entry.Parent || l.entry.Name != b.entry.Name ||
					(!b.entry.Dir && l.entry.Hash != b.entry.Hash) {
					conflicts = append(conflicts, Conflict{id, ConflictDeleteModify})
				} else {
					toLocal = append(toLocal, Action{Kind: ActionDelete, ID: id})
					if b.entry.Dir {
						vetoes = append(vetoes, vetoCandidate{id: id, x: 1, depth: depthOf(local, id)})
					}
				}
			default:
				lLoc := l.entry.Parent != b.entry.Parent || l.entry.Name != b.entry.Name
				rLoc := r.entry.Parent != b.entry.Parent || r.entry.Name != b.entry.Name
				switch {
				case lLoc && rLoc:
					if l.entry.Parent != r.entry.Parent || l.entry.Name != r.entry.Name {
						conflicts = append(conflicts, Conflict{id, ConflictLoc})
					}
				case lLoc:
					toRemote = append(toRemote, Action{Kind: ActionSetLoc, ID: id, Parent: l.entry.Parent, Name: l.entry.Name})
				case rLoc:
					toLocal = append(toLocal, Action{Kind: ActionSetLoc, ID: id, Parent: r.entry.Parent, Name: r.entry.Name})
				}
				if !b.entry.Dir {
					lHash := l.entry.Hash != b.entry.Hash
					rHash := r.entry.Hash != b.entry.Hash
					switch {
					case lHash && rHash:
						if l.entry.Hash != r.entry.Hash {
							conflicts = append(conflicts, Conflict{id, ConflictContent})
						}
					case lHash:
						toRemote = append(toRemote, Action{Kind: ActionSetHash, ID: id, Hash: l.entry.Hash})
					case rHash:
						toLocal = append(toLocal, Action{Kind: ActionSetHash, ID: id, Hash: r.entry.Hash})
					}
				}
			}
		}
	}

	toLocal, toRemote = applyVetoes(local, remote, toLocal, toRemote, vetoes)

	finalL := applyActions(local, toLocal)
	finalR := applyActions(remote, toRemote)
	renL := resolveNameCollisions(finalL)
	renR := resolveNameCollisions(finalR)
	toLocal = foldRenames(toLocal, renL)
	toRemote = foldRenames(toRemote, renR)
	finalL = applyActions(local, toLocal)
	finalR = applyActions(remote, toRemote)

	if !validMerge(finalL) || !validMerge(finalR) {
		return Plan{}, ErrMergeInvalid
	}

	toLocal = orderActions(toLocal, local, finalL)
	toRemote = orderActions(toRemote, remote, finalR)
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].ID != conflicts[j].ID {
			return conflicts[i].ID < conflicts[j].ID
		}
		return conflicts[i].Kind < conflicts[j].Kind
	})

	return Plan{
		ToLocal:   nonNilActions(toLocal),
		ToRemote:  nonNilActions(toRemote),
		Conflicts: nonNilConflicts(conflicts),
	}, nil
}

// Commit plans and, when conflict-free, updates the base.
func (p *Planner) Commit(local, remote Snapshot) (Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	plan, err := p.Plan(local, remote)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.Conflicts) > 0 {
		return Plan{}, ErrHasConflicts
	}
	p.base = applyActions(local, plan.ToLocal)
	return plan, nil
}

func unionIDs(snapshots ...Snapshot) []int64 {
	set := make(map[int64]struct{})
	for _, s := range snapshots {
		for id := range s {
			set[id] = struct{}{}
		}
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func createAction(id int64, e Entry) Action {
	return Action{Kind: ActionCreate, ID: id, Parent: e.Parent, Name: e.Name, Dir: e.Dir, Hash: e.Hash}
}

// depthOf computes one entry's depth in a known-valid snapshot.
func depthOf(s Snapshot, id int64) int {
	d := 0
	for s[id].Parent != 0 {
		id = s[id].Parent
		d++
	}
	return d
}

func nonNilActions(a []Action) []Action {
	if a == nil {
		return []Action{}
	}
	return a
}

func nonNilConflicts(c []Conflict) []Conflict {
	if c == nil {
		return []Conflict{}
	}
	return c
}
