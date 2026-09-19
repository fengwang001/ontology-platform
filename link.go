package ontology

import (
	"fmt"
	"sort"
)

// hasLink reports whether the exact link exists.
func (st *state) hasLink(lt, src, dst string) bool {
	return st.fwd[lt][src][dst]
}

// addLinkRaw inserts the link into both indexes. Callers must have validated.
func (st *state) addLinkRaw(lt, src, dst string) {
	if st.fwd[lt][src] == nil {
		st.fwd[lt][src] = make(map[string]bool)
	}
	st.fwd[lt][src][dst] = true
	if st.rev[lt][dst] == nil {
		st.rev[lt][dst] = make(map[string]bool)
	}
	st.rev[lt][dst][src] = true
}

// removeLinkRaw deletes the link from both indexes; it is a no-op if absent.
func (st *state) removeLinkRaw(lt, src, dst string) {
	if targets := st.fwd[lt][src]; targets != nil {
		delete(targets, dst)
		if len(targets) == 0 {
			delete(st.fwd[lt], src)
		}
	}
	if sources := st.rev[lt][dst]; sources != nil {
		delete(sources, src)
		if len(sources) == 0 {
			delete(st.rev[lt], dst)
		}
	}
}

// linksOf returns every link incident to id (in either role), sorted by
// (linkType, source, target) so cascade planning is deterministic.
func (st *state) linksOf(id string) []linkRef {
	var out []linkRef
	for lt, m := range st.fwd {
		for dst := range m[id] {
			out = append(out, linkRef{lt, id, dst})
		}
	}
	for lt, m := range st.rev {
		for src := range m[id] {
			if src != id { // self-link already collected from fwd
				out = append(out, linkRef{lt, src, id})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].linkType != out[j].linkType {
			return out[i].linkType < out[j].linkType
		}
		if out[i].source != out[j].source {
			return out[i].source < out[j].source
		}
		return out[i].target < out[j].target
	})
	return out
}

// checkEndpoints validates existence and declared types of both endpoints.
func checkEndpoints(st *state, lt *LinkType, src, dst string) error {
	srcType, ok := st.objects[src]
	if !ok {
		return &LinkError{ViolationEndpointNotFound, lt.Name, src, dst,
			fmt.Sprintf("source object %q does not exist", src)}
	}
	dstType, ok := st.objects[dst]
	if !ok {
		return &LinkError{ViolationEndpointNotFound, lt.Name, src, dst,
			fmt.Sprintf("target object %q does not exist", dst)}
	}
	if srcType != lt.Source {
		return &LinkError{ViolationEndpointType, lt.Name, src, dst,
			fmt.Sprintf("source object %q has type %q, want %q", src, srcType, lt.Source)}
	}
	if dstType != lt.Target {
		return &LinkError{ViolationEndpointType, lt.Name, src, dst,
			fmt.Sprintf("target object %q has type %q, want %q", dst, dstType, lt.Target)}
	}
	return nil
}

// checkCardinality enforces ONE_TO_ONE / ONE_TO_MANY against current state.
func checkCardinality(st *state, lt *LinkType, src, dst string) error {
	switch lt.Cardinality {
	case OneToOne:
		if len(st.fwd[lt.Name][src]) > 0 {
			return &LinkError{ViolationOneToOne, lt.Name, src, dst,
				fmt.Sprintf("source %q already has a %s link", src, lt.Name)}
		}
		if len(st.rev[lt.Name][dst]) > 0 {
			return &LinkError{ViolationOneToOne, lt.Name, src, dst,
				fmt.Sprintf("target %q is already linked by %s", dst, lt.Name)}
		}
	case OneToMany:
		if owners := st.rev[lt.Name][dst]; len(owners) > 0 && !owners[src] {
			return &LinkError{ViolationOneToMany, lt.Name, src, dst,
				fmt.Sprintf("target %q already belongs to another source", dst)}
		}
	}
	return nil
}

// createLink is the shared implementation. In idempotent mode (batches) an
// already-existing link is a no-op instead of ViolationDuplicateLink.
func createLink(st *state, linkType, src, dst string, idempotent bool) error {
	lt, ok := st.linkTypes[linkType]
	if !ok {
		return fmt.Errorf("unknown link type %q", linkType)
	}
	if err := checkEndpoints(st, lt, src, dst); err != nil {
		return err
	}
	if st.hasLink(linkType, src, dst) {
		if idempotent {
			return nil
		}
		return &LinkError{ViolationDuplicateLink, linkType, src, dst,
			"link already exists"}
	}
	if err := checkCardinality(st, lt, src, dst); err != nil {
		return err
	}
	st.addLinkRaw(linkType, src, dst)
	return nil
}

// CreateLink creates one link, enforcing endpoint and cardinality rules.
func (s *Store) CreateLink(linkType, source, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return createLink(s.st, linkType, source, target, false)
}

// DeleteLink removes one link. Removing a missing link is an error.
func (s *Store) DeleteLink(linkType, source, target string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.st.linkTypes[linkType]; !ok {
		return fmt.Errorf("unknown link type %q", linkType)
	}
	if !s.st.hasLink(linkType, source, target) {
		return fmt.Errorf("link %q (%s -> %s) does not exist", linkType, source, target)
	}
	s.st.removeLinkRaw(linkType, source, target)
	return nil
}

// LinksFrom returns the sorted targets reachable from source via linkType.
func (s *Store) LinksFrom(linkType, source string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.st.fwd[linkType][source])
}

// LinksTo returns the sorted sources pointing at target via linkType.
func (s *Store) LinksTo(linkType, target string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedKeys(s.st.rev[linkType][target])
}

// HasLink reports whether the exact link exists.
func (s *Store) HasLink(linkType, source, target string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.st.hasLink(linkType, source, target)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
