package ontology

import "sort"

// naiveSnapshot is the independently implemented exhaustive reference
// extractor. Unlike the production extractor it scans every object and
// every link of the state (O(|V|+|E|)) and derives the result from first
// principles. It shares no extraction code with extractState; only the
// model types and ValidID are reused. Its output must match exactly.
func naiveSnapshot(s *graphState, requested []ID, principal string) *Snapshot {
	req := map[ID]bool{}
	for _, id := range requested {
		req[id] = true
	}

	// Existence permission set (independent map construction).
	allowed := map[ID]bool{}
	for pid, objs := range s.grants {
		if pid != principal {
			continue
		}
		for o := range objs {
			allowed[o] = true
		}
	}

	// Full object scan: removal pass first.
	effective := map[ID]bool{}
	removed := map[ID]bool{}
	objects := []Object{}
	for _, id := range requested {
		_, exists := s.objects[id]
		if exists && allowed[id] {
			effective[id] = true
		} else {
			removed[id] = true
		}
	}
	for id := range effective {
		objects = append(objects, s.objects[id])
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ID < objects[j].ID })

	// Full link scan with naive endpoint counting.
	links := []Link{}
	dangling := []DanglingLink{}
	checked := 0
	for _, lk := range s.links {
		incident := effective[lk.From] || effective[lk.To]
		if !incident {
			continue
		}
		checked++
		survivors := 0
		if effective[lk.From] {
			survivors++
		}
		if effective[lk.To] {
			survivors++
		}
		// Self link on an effective endpoint counts as one endpoint but
		// both ends survive; detect explicitly.
		if lk.From == lk.To && effective[lk.From] {
			survivors = 2
		}
		switch survivors {
		case 2:
			links = append(links, lk)
		case 1:
			inc, exc := lk.From, lk.To
			if !effective[inc] {
				inc, exc = lk.To, lk.From
			}
			label := DanglingBoundary
			if removed[exc] {
				label = DanglingPermission
			}
			dangling = append(dangling, DanglingLink{
				LinkID:   lk.ID,
				Type:     lk.Type,
				From:     lk.From,
				To:       lk.To,
				Included: inc,
				Excluded: exc,
				Dir:      lk.DirectionFrom(inc),
				Source:   label,
			})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].ID < links[j].ID })
	sortDangling(dangling)

	scope := append([]ID(nil), requested...)
	sort.Strings(scope)
	if objects == nil {
		objects = []Object{}
	}
	if links == nil {
		links = []Link{}
	}
	if dangling == nil {
		dangling = []DanglingLink{}
	}
	out := &Snapshot{
		Revision:              s.revision,
		Scope:                 scope,
		Principal:             principal,
		Objects:               objects,
		Links:                 links,
		Dangling:              dangling,
		candidateLinksChecked: checked,
	}
	return out
}
