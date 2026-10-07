package ontology

import "sort"

// resolvedScope is the hook-declared read scope with link fields resolved to
// concrete instance IDs, together with the versions observed at resolution
// time. All linked instances named here must be locked by the commit path.
type resolvedScope struct {
	refs      []resolvedRef
	observed  map[ObjectID]Version
	linkedIDs []ObjectID
}

type resolvedRef struct {
	target ObjectID // concrete instance ("" if unresolvable)
	link   Property // link field on the target instance (empty = local)
	prop   Property
}

// resolveLink reads a link property from a snapshot and returns the pointed
// instance ID. Link values are strings (or []byte) encoding an ObjectID.
func resolveLink(snap *snapshot, link Property) ObjectID {
	if snap == nil {
		return ""
	}
	v, ok := snap.Get(link)
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return ObjectID(x)
	case ObjectID:
		return x
	default:
		return ""
	}
}

// resolveScopes invokes every validator's Declare callback against the
// proposed (cloned) values and the baseline snapshot. This happens before any
// locks are taken; map lookups here are read-only and races are handled by
// rechecking state after locking.
func (s *Store) resolveScopes(t TypeName, values map[Property]Value, base *snapshot) (resolvedScope, []string) {
	s.typeMu.RLock()
	hooks := s.types[t]
	s.typeMu.RUnlock()

	rs := resolvedScope{observed: map[ObjectID]Version{}}
	var hookNames []string
	var snapView Snapshot
	if base != nil {
		snapView = base
	}
	for _, h := range hooks {
		if h.Declare == nil {
			continue
		}
		scope := h.Declare(values, snapView)
		hookNames = append(hookNames, h.Name)
		for _, ref := range scope.Refs {
			target := ref.Instance
			if target == "" {
				if ref.Link != "" {
					target = resolveLink(base, ref.Link)
				}
			}
			rs.refs = append(rs.refs, resolvedRef{target: target, link: ref.Link, prop: ref.Local})
			if target != "" {
				rs.observed[target] = 0
			}
		}
		for id, v := range scope.ExternalVersions {
			if id != "" {
				if cur, ok := rs.observed[id]; !ok || v > cur {
					rs.observed[id] = v
				}
			}
		}
	}
	seen := map[ObjectID]bool{}
	for id := range rs.observed {
		if !seen[id] {
			seen[id] = true
			rs.linkedIDs = append(rs.linkedIDs, id)
		}
	}
	sort.Slice(rs.linkedIDs, func(i, j int) bool { return rs.linkedIDs[i] < rs.linkedIDs[j] })
	return rs, hookNames
}

// observeVersions fills in observed versions for linked instances that did
// not pin one explicitly, using the instance head visible at call time.
func (s *Store) observeVersions(rs *resolvedScope, extra map[ObjectID]Version) {
	for id, v := range extra {
		if id == "" {
			continue
		}
		if cur, ok := rs.observed[id]; !ok || v > cur {
			rs.observed[id] = v
		}
	}
	s.objMu.Lock()
	defer s.objMu.Unlock()
	for id := range rs.observed {
		if rs.observed[id] != 0 {
			continue
		}
		if st := s.obj[id]; st != nil {
			st.mu.Lock()
			if st.exists {
				rs.observed[id] = st.head
			}
			st.mu.Unlock()
		}
	}
}
