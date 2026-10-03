package triplesync

import (
	"sort"
	"strconv"
)

// applyVetoes simulates both sides' raw actions and revives unilaterally
// deleted directories that still contain surviving entries on the other side.
// Candidates are judged deepest-first so revived children keep their ancestors
// alive as well.
func applyVetoes(local, remote Snapshot, toLocal, toRemote []Action, vetoes []vetoCandidate) ([]Action, []Action) {
	sort.Slice(vetoes, func(i, j int) bool {
		if vetoes[i].depth != vetoes[j].depth {
			return vetoes[i].depth > vetoes[j].depth
		}
		return vetoes[i].id < vetoes[j].id
	})

	// Each side's alive set starts from the state produced by executing ALL
	// raw actions destined for that side, including pushes of entries created
	// on the other side, except the candidate directories' own pending
	// deletes (their fate is what we are deciding here).
	candidate := make(map[int64]bool, len(vetoes))
	for _, v := range vetoes {
		candidate[v.id] = true
	}
	stripCandidateDeletes := func(acts []Action) []Action {
		out := make([]Action, 0, len(acts))
		for _, a := range acts {
			if a.Kind == ActionDelete && candidate[a.ID] {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	alive := [2]map[int64]Entry{
		applyActions(local, stripCandidateDeletes(toLocal)),
		applyActions(remote, stripCandidateDeletes(toRemote)),
	}

	vetoed := make(map[int64]bool)
	for _, v := range vetoes {
		y := 1 - v.x
		hasChild := false
		for _, e := range alive[y] {
			if e.Parent == v.id {
				hasChild = true
				break
			}
		}
		if !hasChild {
			// The delete stands: the directory is gone on both sides for
			// the purpose of judging its ancestors.
			delete(alive[0], v.id)
			delete(alive[1], v.id)
			continue
		}
		vetoed[v.id] = true
		revived := alive[y][v.id]
		alive[v.x][v.id] = revived
		// The Create is sent to the side that had deleted the directory,
		// using the unchanged side's current location.
		act := createAction(v.id, revived)
		if v.x == 0 {
			toLocal = append(toLocal, act)
		} else {
			toRemote = append(toRemote, act)
		}
	}

	filter := func(acts []Action) []Action {
		out := make([]Action, 0, len(acts))
		for _, a := range acts {
			if a.Kind == ActionDelete && vetoed[a.ID] {
				continue
			}
			out = append(out, a)
		}
		return out
	}
	return filter(toLocal), filter(toRemote)
}

// resolveNameCollisions renames entries in place until every
// (parent, name) is unique. Collision groups are processed in deterministic
// key order; within a group the smallest id keeps the name and losers are
// renamed by ascending id. Renamed entries re-enter later rounds.
func resolveNameCollisions(s Snapshot) map[int64]Entry {
	renames := make(map[int64]Entry)
	type key struct {
		parent int64
		name   string
	}
	for {
		groups := make(map[key][]int64)
		for id, e := range s {
			k := key{e.Parent, e.Name}
			groups[k] = append(groups[k], id)
		}
		var dupKeys []key
		for k, members := range groups {
			if len(members) > 1 {
				dupKeys = append(dupKeys, k)
			}
		}
		if len(dupKeys) == 0 {
			break
		}
		sort.Slice(dupKeys, func(i, j int) bool {
			if dupKeys[i].parent != dupKeys[j].parent {
				return dupKeys[i].parent < dupKeys[j].parent
			}
			return dupKeys[i].name < dupKeys[j].name
		})
		k := dupKeys[0]
		members := groups[k]
		sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
		losers := append([]int64(nil), members[1:]...)
		for _, id := range losers {
			e := s[id]
			name := e.Name + ".c" + strconv.FormatInt(id, 10)
			for occupiedName(s, e.Parent, name, id) {
				name += "x"
			}
			e.Name = name
			s[id] = e
			renames[id] = e
		}
	}
	return renames
}

func occupiedName(s Snapshot, parent int64, name string, except int64) bool {
	for id, e := range s {
		if id != except && e.Parent == parent && e.Name == name {
			return true
		}
	}
	return false
}

// foldRenames expresses renames through the side's existing Create/SetLoc
// action when possible, otherwise appends a SetLoc.
func foldRenames(acts []Action, renames map[int64]Entry) []Action {
	for i := range acts {
		if rn, ok := renames[acts[i].ID]; ok && (acts[i].Kind == ActionCreate || acts[i].Kind == ActionSetLoc) {
			acts[i].Parent = rn.Parent
			acts[i].Name = rn.Name
			delete(renames, acts[i].ID)
		}
	}
	ids := make([]int64, 0, len(renames))
	for id := range renames {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		e := renames[id]
		acts = append(acts, Action{Kind: ActionSetLoc, ID: id, Parent: e.Parent, Name: e.Name})
	}
	return acts
}

// orderActions enforces: creates by post-execution depth asc then id asc;
// then SetLoc/SetHash by id asc (SetLoc before SetHash for the same id);
// finally deletes by pre-execution depth desc then id asc.
func orderActions(acts []Action, pre, post Snapshot) []Action {
	var creates, sets, deletes []Action
	for _, a := range acts {
		switch a.Kind {
		case ActionCreate:
			creates = append(creates, a)
		case ActionDelete:
			deletes = append(deletes, a)
		default:
			sets = append(sets, a)
		}
	}
	postDepth := depths(post)
	sort.Slice(creates, func(i, j int) bool {
		di, dj := postDepth[creates[i].ID], postDepth[creates[j].ID]
		if di != dj {
			return di < dj
		}
		return creates[i].ID < creates[j].ID
	})
	sort.Slice(sets, func(i, j int) bool {
		if sets[i].ID != sets[j].ID {
			return sets[i].ID < sets[j].ID
		}
		return sets[i].Kind < sets[j].Kind
	})
	sort.Slice(deletes, func(i, j int) bool {
		di, dj := depthOf(pre, deletes[i].ID), depthOf(pre, deletes[j].ID)
		if di != dj {
			return di > dj
		}
		return deletes[i].ID < deletes[j].ID
	})
	out := make([]Action, 0, len(acts))
	out = append(out, creates...)
	out = append(out, sets...)
	out = append(out, deletes...)
	return out
}
