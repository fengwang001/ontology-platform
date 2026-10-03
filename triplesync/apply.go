package triplesync

// applyActions returns a copy of s with the given actions executed in order.
// The resulting snapshot is not re-validated; callers check validity once the
// full action list (including renames) is known.
func applyActions(s Snapshot, actions []Action) Snapshot {
	out := make(Snapshot, len(s)+len(actions))
	for id, e := range s {
		out[id] = e
	}
	for _, a := range actions {
		switch a.Kind {
		case ActionCreate:
			out[a.ID] = Entry{Parent: a.Parent, Name: a.Name, Dir: a.Dir, Hash: a.Hash}
		case ActionSetLoc:
			e := out[a.ID]
			e.Parent = a.Parent
			e.Name = a.Name
			out[a.ID] = e
		case ActionSetHash:
			e := out[a.ID]
			e.Hash = a.Hash
			out[a.ID] = e
		case ActionDelete:
			delete(out, a.ID)
		}
	}
	return out
}

// validMerge checks the merged snapshot for cycles, dangling parents and
// duplicate sibling names. Dir/hash conventions of merged states are the same
// as for input snapshots, so they are checked as well.
func validMerge(s Snapshot) bool {
	return validateSnapshot(s)
}
