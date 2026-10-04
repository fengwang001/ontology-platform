package ontology

import "sort"

func orderActions(actions []Action, before, after Snapshot) []Action {
	creates := make([]Action, 0)
	sets := make([]Action, 0)
	deletes := make([]Action, 0)

	for _, action := range actions {
		switch action.Op {
		case Create:
			creates = append(creates, action)
		case SetLoc, SetHash:
			sets = append(sets, action)
		case Delete:
			deletes = append(deletes, action)
		}
	}

	sort.Slice(creates, func(i, j int) bool {
		left := depthInSnapshot(after, creates[i].ID)
		right := depthInSnapshot(after, creates[j].ID)
		if left != right {
			return left < right
		}
		return creates[i].ID < creates[j].ID
	})

	sort.Slice(sets, func(i, j int) bool {
		if sets[i].ID != sets[j].ID {
			return sets[i].ID < sets[j].ID
		}
		return sets[i].Op == SetLoc && sets[j].Op == SetHash
	})

	sort.Slice(deletes, func(i, j int) bool {
		left := depthInSnapshot(before, deletes[i].ID)
		right := depthInSnapshot(before, deletes[j].ID)
		if left != right {
			return left > right
		}
		return deletes[i].ID < deletes[j].ID
	})

	return append(append(creates, sets...), deletes...)
}
