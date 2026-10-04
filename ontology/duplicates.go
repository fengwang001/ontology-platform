package ontology

import "sort"

type rename struct {
	Parent int64
	Name   string
}

func resolveDuplicates(snapshot Snapshot) (Snapshot, map[int64]rename) {
	state := cloneSnapshot(snapshot)
	renames := make(map[int64]rename)

	for {
		groups := make(map[locationKey][]int64)
		for id, entry := range state {
			key := locationKey{Parent: entry.Parent, Name: entry.Name}
			groups[key] = append(groups[key], id)
		}

		keys := make([]locationKey, 0, len(groups))
		for key, ids := range groups {
			if len(ids) > 1 {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			break
		}

		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Parent != keys[j].Parent {
				return keys[i].Parent < keys[j].Parent
			}
			return keys[i].Name < keys[j].Name
		})

		changed := false
		for _, key := range keys {
			ids := groups[key]
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			for _, id := range ids[1:] {
				entry := state[id]
				entry.Name = uniqueName(state, entry.Parent, entry.Name, id)
				state[id] = entry
				renames[id] = rename{Parent: entry.Parent, Name: entry.Name}
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return state, renames
}

func uniqueName(snapshot Snapshot, parent int64, baseName string, self int64) string {
	name := baseName + ".c" + decimal(self)
	for nameExistsExcept(snapshot, parent, name, self) {
		name += "x"
	}
	return name
}

func nameExistsExcept(snapshot Snapshot, parent int64, name string, self int64) bool {
	for id, entry := range snapshot {
		if id != self && entry.Parent == parent && entry.Name == name {
			return true
		}
	}
	return false
}

func decimal(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func applyRenames(actions []Action, state Snapshot, renames map[int64]rename) []Action {
	result := make([]Action, 0, len(actions)+len(renames))
	seen := make(map[int64]bool)

	for _, action := range actions {
		if update, ok := renames[action.ID]; ok && action.Op == Create {
			action.Parent = update.Parent
			action.Name = update.Name
			seen[action.ID] = true
		}
		if update, ok := renames[action.ID]; ok && action.Op == SetLoc {
			action.Parent = update.Parent
			action.Name = update.Name
			seen[action.ID] = true
		}
		result = append(result, action)
	}

	ids := make([]int64, 0, len(renames))
	for id := range renames {
		if !seen[id] {
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
