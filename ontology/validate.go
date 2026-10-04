package ontology

import "strings"

func validateSnapshot(snapshot Snapshot) bool {
	ids := sortedIDs(snapshot)
	locations := make(map[locationKey]bool, len(ids))

	for _, id := range ids {
		if id < 1 {
			return false
		}

		entry := snapshot[id]
		if entry.Name == "" || strings.Contains(entry.Name, "/") {
			return false
		}
		if entry.Dir {
			if entry.Hash != "" {
				return false
			}
		} else if entry.Hash == "" {
			return false
		}

		if entry.Parent != 0 {
			parent, ok := snapshot[entry.Parent]
			if !ok || !parent.Dir {
				return false
			}
		}

		key := locationKey{Parent: entry.Parent, Name: entry.Name}
		if locations[key] {
			return false
		}
		locations[key] = true
	}

	for _, id := range ids {
		seen := map[int64]bool{id: true}
		parent := snapshot[id].Parent
		for parent != 0 {
			if seen[parent] {
				return false
			}
			seen[parent] = true
			parent = snapshot[parent].Parent
		}
	}

	return true
}

func dirsAgree(base, local, remote Snapshot) (bool, bool) {
	for id := range base {
		if left, ok := local[id]; ok && left.Dir != base[id].Dir {
			return false, true
		}
		if right, ok := remote[id]; ok && right.Dir != base[id].Dir {
			return true, false
		}
	}

	for id := range local {
		if _, inBase := base[id]; inBase {
			continue
		}
		if right, ok := remote[id]; ok && right.Dir != local[id].Dir {
			return true, false
		}
	}

	return true, true
}

type locationKey struct {
	Parent int64
	Name   string
}
