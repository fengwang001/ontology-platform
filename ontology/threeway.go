package ontology

import "sort"

func buildRawPlan(base, local, remote Snapshot) (toLocal, toRemote []Action, conflicts []Conflict) {
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
			if locDiffers(b, l) || contentDiffers(b, l) {
				conflicts = append(conflicts, Conflict{ID: id, Kind: DeleteModify})
			} else {
				toLocal = append(toLocal, Action{Op: Delete, ID: id})
			}
		case bOK && !lOK && rOK:
			if locDiffers(b, r) || contentDiffers(b, r) {
				conflicts = append(conflicts, Conflict{ID: id, Kind: DeleteModify})
			} else {
				toRemote = append(toRemote, Action{Op: Delete, ID: id})
			}
		case bOK && !lOK && !rOK:
		case bOK && lOK && rOK:
			locChanged := locDiffers(b, l)
			locOther := locDiffers(b, r)
			contentChanged := contentDiffers(b, l)
			contentOther := contentDiffers(b, r)

			if locChanged || locOther {
				switch {
				case locChanged && locOther && l.Parent == r.Parent && l.Name == r.Name:
				case locChanged && !locOther:
					toRemote = append(toRemote, Action{Op: SetLoc, ID: id, Parent: l.Parent, Name: l.Name})
				case !locChanged && locOther:
					toLocal = append(toLocal, Action{Op: SetLoc, ID: id, Parent: r.Parent, Name: r.Name})
				default:
					conflicts = append(conflicts, Conflict{ID: id, Kind: Loc})
				}
			}

			if !b.Dir && (contentChanged || contentOther) {
				switch {
				case contentChanged && contentOther && l.Hash == r.Hash:
				case contentChanged && !contentOther:
					toRemote = append(toRemote, Action{Op: SetHash, ID: id, Hash: l.Hash})
				case !contentChanged && contentOther:
					toLocal = append(toLocal, Action{Op: SetHash, ID: id, Hash: r.Hash})
				default:
					conflicts = append(conflicts, Conflict{ID: id, Kind: Content})
				}
			}
		case !bOK && lOK && rOK:
			mergeBothCreate(id, l, r, &conflicts)
		}
	}
	return
}

func unionIDs(snapshots ...Snapshot) []int64 {
	seen := make(map[int64]bool)
	ids := make([]int64, 0)
	for _, snapshot := range snapshots {
		for id := range snapshot {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func mergeBothCreate(id int64, local, remote Entry, conflicts *[]Conflict) {
	if local.Parent != remote.Parent || local.Name != remote.Name {
		*conflicts = append(*conflicts, Conflict{ID: id, Kind: Loc})
	}
	if !local.Dir && local.Hash != remote.Hash {
		*conflicts = append(*conflicts, Conflict{ID: id, Kind: Content})
	}
}

func createAction(id int64, entry Entry) Action {
	return Action{Op: Create, ID: id, Parent: entry.Parent, Name: entry.Name, Dir: entry.Dir, Hash: entry.Hash}
}

func localChanged(base, local Entry) bool {
	return locDiffers(base, local) || contentDiffers(base, local)
}

func remoteChanged(base, remote Entry) bool {
	return localChanged(base, remote)
}

func locDiffers(a, b Entry) bool {
	return a.Parent != b.Parent || a.Name != b.Name
}

func contentDiffers(a, b Entry) bool {
	if a.Dir || b.Dir {
		return false
	}
	return a.Hash != b.Hash
}
