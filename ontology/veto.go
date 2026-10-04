package ontology

import "sort"

func vetoDirectoryDeletes(before Snapshot, targetActions, otherActions *[]Action) {
	type candidate struct {
		id    int64
		depth int
	}

	candidates := make([]candidate, 0)
	for _, action := range *targetActions {
		if action.Op != Delete {
			continue
		}
		entry, ok := before[action.ID]
		if ok && entry.Dir {
			candidates = append(candidates, candidate{id: action.ID, depth: depthInSnapshot(before, action.ID)})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].depth != candidates[j].depth {
			return candidates[i].depth > candidates[j].depth
		}
		return candidates[i].id < candidates[j].id
	})

	vetoed := make(map[int64]bool)
	for _, item := range candidates {
		actions := make([]Action, 0, len(*targetActions))
		for _, action := range *targetActions {
			if action.Op == Delete && (vetoed[action.ID] || action.ID == item.id) {
				continue
			}
			actions = append(actions, action)
		}

		state := applyActions(before, actions)
		if hasChild(state, item.id) {
			vetoed[item.id] = true
			entry := before[item.id]
			*otherActions = append(*otherActions, createAction(item.id, entry))
		}
	}

	if len(vetoed) == 0 {
		return
	}

	kept := make([]Action, 0, len(*targetActions))
	for _, action := range *targetActions {
		if action.Op == Delete && vetoed[action.ID] {
			continue
		}
		kept = append(kept, action)
	}
	*targetActions = kept
}

func hasChild(snapshot Snapshot, parent int64) bool {
	for _, entry := range snapshot {
		if entry.Parent == parent {
			return true
		}
	}
	return false
}

func depthInSnapshot(snapshot Snapshot, id int64) int {
	depth := 0
	seen := map[int64]bool{id: true}
	parent := snapshot[id].Parent
	for parent != 0 {
		depth++
		entry, ok := snapshot[parent]
		if !ok || seen[parent] {
			return depth
		}
		seen[parent] = true
		parent = entry.Parent
	}
	return depth
}
