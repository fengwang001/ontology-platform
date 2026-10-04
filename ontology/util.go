package ontology

import "sort"

func sortedIDs(snapshot Snapshot) []int64 {
	ids := make([]int64, 0, len(snapshot))
	for id := range snapshot {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := make(Snapshot, len(snapshot))
	for id, entry := range snapshot {
		clone[id] = entry
	}
	return clone
}
