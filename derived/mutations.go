package derived

import "fmt"

// AddObject registers a new object instance. Properties are copied.
func (s *Store) AddObject(obj Object) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.cur.objects[obj.ID]; exists {
		return fmt.Errorf("derived: object %q already exists", obj.ID)
	}
	props := make(map[string]Value, len(obj.Properties))
	for k, v := range obj.Properties {
		props[k] = v
	}
	s.cur.objects[obj.ID] = &Object{ID: obj.ID, Type: obj.Type, Properties: props}
	return nil
}

// AddDeclaration registers a derived index. The declaration reference graph
// must remain acyclic; a cyclic or inconsistent declaration is rejected.
func (s *Store) AddDeclaration(d Declaration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Name == "" {
		return fmt.Errorf("derived: declaration name is empty")
	}
	if _, exists := s.cur.declarations[d.Name]; exists {
		return fmt.Errorf("derived: declaration %q already exists", d.Name)
	}
	if d.LinkType == "" {
		return fmt.Errorf("derived: declaration %q has empty link type", d.Name)
	}
	if sub, isDecl := s.cur.declarations[d.SourceProperty]; isDecl {
		if d.SourceType != sub.DownstreamType {
			return fmt.Errorf("derived: declaration %q chains %q with mismatched source type %q != %q",
				d.Name, sub.Name, d.SourceType, sub.DownstreamType)
		}
	}
	s.cur.declarations[d.Name] = &d
	s.cur.declByLink[d.LinkType] = append(s.cur.declByLink[d.LinkType], &d)
	return nil
}

// commit validates and applies fn to a cloned candidate snapshot, recomputes
// every affected entry (simulating downstream update failures via FailHook),
// and publishes the snapshot only when the entire unit succeeds.
func (s *Store) commit(rec *ChangeRecord, fn func(c *state) (AffectedSet, *IndexError)) *ChangeResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	candidate := s.cur.clone()
	affected, idxErr := fn(candidate)
	if idxErr != nil {
		rec.RolledBack = true
		rec.Err = idxErr
		s.log(rec)
		return &ChangeResult{Committed: false, Err: idxErr}
	}

	entries := make([]Entry, 0)
	for _, id := range affected.IDs() {
		names := make([]string, 0, len(affected[id]))
		for name := range affected[id] {
			names = append(names, name)
		}
		sortStrings(names)
		for _, name := range names {
			if s.FailHook != nil && s.FailHook(name, id) {
				err := &IndexError{Kind: KindDownstreamUpdateFailed,
					Message: fmt.Sprintf("derived: downstream index update for %s on %s failed", name, id)}
				rec.RolledBack = true
				rec.Err = err
				s.log(rec)
				return &ChangeResult{Committed: false, Err: err}
			}
			d := candidate.declarations[name]
			entries = append(entries, computeEntry(candidate, d, id))
		}
	}

	s.cur = candidate
	rec.Affected = affected.IDs()
	rec.Entries = entries
	s.log(rec)
	return &ChangeResult{Committed: true, Affected: rec.Affected, Entries: entries}
}

// SetProperty writes a base property. Every currently depending downstream
// entry is updated in the same processing unit; any failing update rolls the
// whole write back.
func (s *Store) SetProperty(objID, prop string, val Value) *ChangeResult {
	rec := &ChangeRecord{
		Op:    "SetProperty",
		Input: map[string]any{"object": objID, "property": prop, "value": val},
	}
	return s.commit(rec, func(c *state) (AffectedSet, *IndexError) {
		if _, ok := c.objects[objID]; !ok {
			return nil, &IndexError{Kind: KindSourceNotFound,
				Message: fmt.Sprintf("derived: source object %q does not exist", objID)}
		}
		affected := affectedByPropertyChange(c, objID, prop)
		c.objects[objID].Properties[prop] = val
		rec.Basis = "reverse value-flow BFS seeded with base property, traversing in-adjacency along declaration links"
		return affected, nil
	})
}

// validateLink checks structural constraints by fixed priority. Both
// endpoints must exist and the type pair must match a declaration using typ.
func validateLink(c *state, from, to, typ string) *IndexError {
	var errs []*IndexError
	fromObj := c.objects[from]
	toObj := c.objects[to]
	if toObj == nil {
		errs = append(errs, &IndexError{Kind: KindSourceNotFound,
			Message: fmt.Sprintf("derived: link target %q does not exist", to)})
	}
	decls := c.declByLink[typ]
	if len(decls) == 0 {
		errs = append(errs, &IndexError{Kind: KindUnsupportedLinkType,
			Message: fmt.Sprintf("derived: link type %q supports no derived index", typ)})
		return highestPriorityError(errs)
	}
	supported := false
	for _, d := range decls {
		fromOK := fromObj != nil && fromObj.Type == d.DownstreamType
		toOK := toObj != nil && toObj.Type == d.SourceType
		if fromOK && toOK {
			supported = true
			break
		}
	}
	if !supported {
		errs = append(errs, &IndexError{Kind: KindUnsupportedLinkType,
			Message: fmt.Sprintf("derived: link type %q is not supported between these object types", typ)})
	}
	return highestPriorityError(errs)
}

// AddLink creates a link and updates the affected entries in one unit. Links
// that would close an instance-level derivation cycle are rejected before
// becoming visible.
func (s *Store) AddLink(from, to, typ string) *ChangeResult {
	rec := &ChangeRecord{
		Op:    "AddLink",
		Input: map[string]any{"from": from, "to": to, "type": typ},
	}
	return s.commit(rec, func(c *state) (AffectedSet, *IndexError) {
		if _, ok := c.objects[from]; !ok {
			return nil, &IndexError{Kind: KindSourceNotFound,
				Message: fmt.Sprintf("derived: link source %q does not exist", from)}
		}
		if err := validateLink(c, from, to, typ); err != nil {
			return nil, err
		}
		if createsCycle(c, from, to, typ) {
			return nil, &IndexError{Kind: KindCycleDetected,
				Message: fmt.Sprintf("derived: link %s --%s--> %s would form a transitive cycle", from, typ, to)}
		}
		affected := linkChangeAffected(c, from, typ)
		c.addLink(from, to, typ)
		rec.Basis = "owner entry for declarations traversing the link, then reverse BFS through the derived property"
		return affected, nil
	})
}

// DeleteLink removes a link and updates affected entries in one unit.
func (s *Store) DeleteLink(from, to, typ string) *ChangeResult {
	rec := &ChangeRecord{
		Op:    "DeleteLink",
		Input: map[string]any{"from": from, "to": to, "type": typ},
	}
	return s.commit(rec, func(c *state) (AffectedSet, *IndexError) {
		if _, ok := c.objects[from]; !ok {
			return nil, &IndexError{Kind: KindSourceNotFound,
				Message: fmt.Sprintf("derived: link source %q does not exist", from)}
		}
		if err := validateLink(c, from, to, typ); err != nil {
			return nil, err
		}
		affected := linkChangeAffected(c, from, typ)
		c.removeLink(from, to, typ)
		rec.Basis = "owner entry for declarations traversing the link, then reverse BFS through the derived property"
		return affected, nil
	})
}

// linkChangeAffected computes entries that can change when a typ-link of
// `from` is added or removed: the owner's own entries for declarations on
// typ, seeded as a derived-property change so upstream multi-level
// dependents are found by the same reverse BFS.
func linkChangeAffected(c *state, from, typ string) AffectedSet {
	seeds := AffectedSet{}
	nodes := []bfsNode{}
	for _, d := range c.declByLink[typ] {
		obj := c.objects[from]
		if obj == nil || obj.Type != d.DownstreamType {
			continue
		}
		if seeds[from] == nil {
			seeds[from] = map[string]struct{}{}
		}
		if _, dup := seeds[from][d.Name]; !dup {
			seeds[from][d.Name] = struct{}{}
			nodes = append(nodes, bfsNode{id: from, prop: d.Name})
		}
	}
	return propagate(c, nodes, seeds)
}

// DeleteObject removes an instance. Dependents turn non-indexable in the same
// unit; deleting a downstream removes only its own entries and never touches
// co-downstreams that share the same source.
func (s *Store) DeleteObject(id string) *ChangeResult {
	rec := &ChangeRecord{
		Op:    "DeleteObject",
		Input: map[string]any{"object": id},
	}
	return s.commit(rec, func(c *state) (AffectedSet, *IndexError) {
		obj := c.objects[id]
		if obj == nil {
			return nil, &IndexError{Kind: KindSourceNotFound,
				Message: fmt.Sprintf("derived: object %q does not exist", id)}
		}

		var nodes []bfsNode
		seeds := AffectedSet{}
		for prop := range obj.Properties {
			nodes = append(nodes, bfsNode{id: id, prop: prop})
		}
		for _, d := range declarationsForObject(c, id) {
			nodes = append(nodes, bfsNode{id: id, prop: d.Name})
			if seeds[id] == nil {
				seeds[id] = map[string]struct{}{}
			}
			seeds[id][d.Name] = struct{}{}
		}
		affected := propagate(c, nodes, seeds)

		for k := range c.links {
			if k.from == id || k.to == id {
				delete(c.links, k)
			}
		}
		c.removeIncidentLinks(id)
		delete(c.objects, id)
		delete(affected, id)

		rec.Basis = "base properties and own derived entries of the deleted instance as seeds; reverse BFS reaches dependents"
		return affected, nil
	})
}
