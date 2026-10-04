package ontology

func buildPlan(base, local, remote Snapshot) (PlanResult, error) {
	toLocal, toRemote, conflicts := buildRawPlan(base, local, remote)
	localBefore := cloneSnapshot(local)
	remoteBefore := cloneSnapshot(remote)

	vetoDirectoryDeletes(localBefore, &toLocal, &toRemote)
	vetoDirectoryDeletes(remoteBefore, &toRemote, &toLocal)

	localState, localRename := resolveDuplicates(applyActions(localBefore, toLocal))
	remoteState, remoteRename := resolveDuplicates(applyActions(remoteBefore, toRemote))

	if !validateSnapshot(localState) || !validateSnapshot(remoteState) {
		return PlanResult{}, ErrMergeInvalid
	}

	toLocal = applyRenames(toLocal, localState, localRename)
	toRemote = applyRenames(toRemote, remoteState, remoteRename)

	toLocal = orderActions(toLocal, localBefore, localState)
	toRemote = orderActions(toRemote, remoteBefore, remoteState)

	return PlanResult{
		ToLocal:   cloneActions(toLocal),
		ToRemote:  cloneActions(toRemote),
		Conflicts: cloneConflicts(conflicts),
	}, nil
}

func cloneActions(actions []Action) []Action {
	return append([]Action(nil), actions...)
}

func cloneConflicts(conflicts []Conflict) []Conflict {
	return append([]Conflict(nil), conflicts...)
}

func applyActions(snapshot Snapshot, actions []Action) Snapshot {
	result := cloneSnapshot(snapshot)
	for _, action := range actions {
		switch action.Op {
		case Create:
			result[action.ID] = Entry{Parent: action.Parent, Name: action.Name, Dir: action.Dir, Hash: action.Hash}
		case Delete:
			delete(result, action.ID)
		case SetLoc:
			entry := result[action.ID]
			entry.Parent = action.Parent
			entry.Name = action.Name
			result[action.ID] = entry
		case SetHash:
			entry := result[action.ID]
			entry.Hash = action.Hash
			result[action.ID] = entry
		}
	}
	return result
}
