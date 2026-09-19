package ontology

import "fmt"

// deletePlan is the full effect of a cascade delete, computed before any
// mutation. If planning fails (RESTRICT), nothing is applied, which gives
// total rollback for free.
type deletePlan struct {
	objects map[string]bool
	unlinks map[linkRef]bool
}

// planDelete walks the cascade graph from root. The visited set
// (plan.objects) guarantees termination on cycles and that every object is
// settled exactly once.
func planDelete(st *state, root string) (*deletePlan, error) {
	if _, ok := st.objects[root]; !ok {
		return nil, fmt.Errorf("object %q does not exist", root)
	}
	p := &deletePlan{objects: make(map[string]bool), unlinks: make(map[linkRef]bool)}
	if err := visitDelete(st, root, nil, p); err != nil {
		return nil, err
	}
	return p, nil
}

func visitDelete(st *state, id string, path []PathStep, p *deletePlan) error {
	if p.objects[id] {
		return nil
	}
	p.objects[id] = true
	for _, ref := range st.linksOf(id) {
		lt := st.linkTypes[ref.linkType]
		other := ref.source
		if other == id {
			other = ref.target
		}
		step := PathStep{From: id, LinkType: ref.linkType, To: other}
		switch lt.OnDelete {
		case Cascade:
			if other == id {
				continue // self-link: both endpoints are the same object
			}
			if err := visitDelete(st, other, append(path, step), p); err != nil {
				return err
			}
		case SetNull:
			p.unlinks[ref] = true
		case Restrict:
			return &RestrictError{
				Path:     append(path, step),
				LinkType: ref.linkType,
				Source:   ref.source,
				Target:   ref.target,
			}
		}
	}
	return nil
}

// applyDelete executes a validated plan: drop SET_NULL links, then delete
// objects together with every link still incident to them.
func applyDelete(st *state, p *deletePlan) {
	for ref := range p.unlinks {
		st.removeLinkRaw(ref.linkType, ref.source, ref.target)
	}
	for id := range p.objects {
		removeObject(st, id)
	}
}

// removeObject deletes one object and every link still touching it.
func removeObject(st *state, id string) {
	for _, ref := range st.linksOf(id) {
		st.removeLinkRaw(ref.linkType, ref.source, ref.target)
	}
	delete(st.objects, id)
}

// DeleteObject removes an object and settles every incident link according
// to its LinkType's cascade mode. CASCADE recurses (cycle-safe); RESTRICT
// anywhere in the cascade chain aborts the whole delete with zero effects
// and a RestrictError carrying the full path from id to the restricted link.
func (s *Store) DeleteObject(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, err := planDelete(s.st, id)
	if err != nil {
		return err
	}
	applyDelete(s.st, plan)
	return nil
}
