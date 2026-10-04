package ontology

import "sort"

func naivePlan(base, local, remote Snapshot) naiveResult {
	var toLocal []Action
	var toRemote []Action
	var conflicts []Conflict

	for _, id := range unionIDs(base, local, remote) {
		b, bOK := base[id]
		l, lOK := local[id]
		r, rOK := remote[id]

		switch {
		case !bOK && lOK && !rOK:
			toRemote = append(toRemote, createAction(id, l))
		case !bOK && !lOK && rOK:
			toLocal = append(toLocal, createAction(id, r))
		case bOK && lOK && !rOK:
			if localChanged(b, l) {
				conflicts = append(conflicts, Conflict{ID: id, Kind: DeleteModify})
			} else {
				toLocal = append(toLocal, Action{Op: Delete, ID: id})
			}
		case bOK && !lOK && rOK:
			if remoteChanged(b, r) {
				conflicts = append(conflicts, Conflict{ID: id, Kind: DeleteModify})
			} else {
				toRemote = append(toRemote, Action{Op: Delete, ID: id})
			}
		case bOK && lOK && rOK:
			lLoc, rLoc := locDiffers(b, l), locDiffers(b, r)
			lHash, rHash := contentDiffers(b, l), contentDiffers(b, r)
			if lLoc != rLoc {
				if lLoc {
					toRemote = append(toRemote, Action{Op: SetLoc, ID: id, Parent: l.Parent, Name: l.Name})
				} else {
					toLocal = append(toLocal, Action{Op: SetLoc, ID: id, Parent: r.Parent, Name: r.Name})
				}
			} else if lLoc && (l.Parent != r.Parent || l.Name != r.Name) {
				conflicts = append(conflicts, Conflict{ID: id, Kind: Loc})
			}
			if lHash != rHash {
				if lHash {
					toRemote = append(toRemote, Action{Op: SetHash, ID: id, Hash: l.Hash})
				} else {
					toLocal = append(toLocal, Action{Op: SetHash, ID: id, Hash: r.Hash})
				}
			} else if lHash && l.Hash != r.Hash {
				conflicts = append(conflicts, Conflict{ID: id, Kind: Content})
			}
		case !bOK && lOK && rOK:
			if l.Parent != r.Parent || l.Name != r.Name {
				conflicts = append(conflicts, Conflict{ID: id, Kind: Loc})
			}
			if !l.Dir && l.Hash != r.Hash {
				conflicts = append(conflicts, Conflict{ID: id, Kind: Content})
			}
		}
	}

	naiveVeto(local, &toLocal, &toRemote)
	naiveVeto(remote, &toRemote, &toLocal)

	localState, localRenames := naiveResolve(naiveApply(local, toLocal))
	remoteState, remoteRenames := naiveResolve(naiveApply(remote, toRemote))
	if !validateSnapshot(localState) || !validateSnapshot(remoteState) {
		return naiveResult{conflicts: conflicts, err: ErrMergeInvalid}
	}

	toLocal = naiveRewrite(toLocal, localRenames)
	toRemote = naiveRewrite(toRemote, remoteRenames)
	toLocal = orderActions(toLocal, local, localState)
	toRemote = orderActions(toRemote, remote, remoteState)

	return naiveResult{toLocal: toLocal, toRemote: toRemote, conflicts: conflicts}
}

func naiveApply(before Snapshot, actions []Action) Snapshot {
	state := cloneSnapshot(before)
	for _, action := range actions {
		switch action.Op {
		case Create:
			state[action.ID] = Entry{Parent: action.Parent, Name: action.Name, Dir: action.Dir, Hash: action.Hash}
		case Delete:
			delete(state, action.ID)
		case SetLoc:
			entry := state[action.ID]
			entry.Parent, entry.Name = action.Parent, action.Name
			state[action.ID] = entry
		case SetHash:
			entry := state[action.ID]
			entry.Hash = action.Hash
			state[action.ID] = entry
		}
	}
	return state
}

func naiveVeto(before Snapshot, targetActions, otherActions *[]Action) {
	type candidate struct {
		id    int64
		depth int
	}
	var candidates []candidate
	for _, action := range *targetActions {
		if action.Op == Delete && before[action.ID].Dir {
			candidates = append(candidates, candidate{action.ID, depthInSnapshot(before, action.ID)})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth > candidates[j].depth
		}
		return candidates[i].id < candidates[j].id
	})

	vetoed := make(map[int64]bool)
	for _, candidate := range candidates {
		var trial []Action
		for _, action := range *targetActions {
			if action.Op == Delete && (vetoed[action.ID] || action.ID == candidate.id) {
				continue
			}
			trial = append(trial, action)
		}
		state := naiveApply(before, trial)
		if hasChild(state, candidate.id) {
			vetoed[candidate.id] = true
			*otherActions = append(*otherActions, createAction(candidate.id, before[candidate.id]))
		}
	}

	var kept []Action
	for _, action := range *targetActions {
		if action.Op == Delete && vetoed[action.ID] {
			continue
		}
		kept = append(kept, action)
	}
	*targetActions = kept
}

func naiveResolve(before Snapshot) (Snapshot, map[int64]rename) {
	state := cloneSnapshot(before)
	renames := make(map[int64]rename)
	for {
		groups := make(map[locationKey][]int64)
		for id, entry := range state {
			key := locationKey{entry.Parent, entry.Name}
			groups[key] = append(groups[key], id)
		}
		var duplicateKeys []locationKey
		for key, ids := range groups {
			if len(ids) > 1 {
				duplicateKeys = append(duplicateKeys, key)
			}
		}
		if len(duplicateKeys) == 0 {
			return state, renames
		}
		sort.Slice(duplicateKeys, func(i, j int) bool {
			if duplicateKeys[i].Parent != duplicateKeys[j].Parent {
				return duplicateKeys[i].Parent < duplicateKeys[j].Parent
			}
			return duplicateKeys[i].Name < duplicateKeys[j].Name
		})
		for _, key := range duplicateKeys {
			ids := append([]int64(nil), groups[key]...)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids[1:] {
				entry := state[id]
				name := entry.Name + ".c" + keyID(id)
				for nameExistsExcept(state, entry.Parent, name, id) {
					name += "x"
				}
				entry.Name = name
				state[id] = entry
				renames[id] = rename{Parent: entry.Parent, Name: name}
			}
		}
	}
}

func naiveRewrite(actions []Action, renames map[int64]rename) []Action {
	var result []Action
	covered := make(map[int64]bool)
	for _, action := range actions {
		if update, ok := renames[action.ID]; ok && action.Op == Create {
			action.Parent, action.Name = update.Parent, update.Name
			covered[action.ID] = true
		}
		if update, ok := renames[action.ID]; ok && action.Op == SetLoc {
			action.Parent, action.Name = update.Parent, update.Name
			covered[action.ID] = true
		}
		result = append(result, action)
	}
	var ids []int64
	for id := range renames {
		if !covered[id] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		update := renames[id]
		result = append(result, Action{Op: SetLoc, ID: id, Parent: update.Parent, Name: update.Name})
	}
	return result
}
