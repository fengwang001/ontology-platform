package ontology

// Permission operations are intentionally separated conceptually from graph
// mutations but share Graph.mu, so a query snapshot always sees a state
// equivalent to one point in a single serial order of all operations.

// GrantExistence lets caller know that objectID exists.
func (g *Graph) GrantExistence(caller, objectID ID) error {
	if err := ValidateID(caller); err != nil {
		return err
	}
	if err := ValidateID(objectID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	set, ok := g.existence[caller]
	if !ok {
		set = map[ID]bool{}
		g.existence[caller] = set
	}
	if !set[objectID] {
		set[objectID] = true
		g.seq++
	}
	return nil
}

// RevokeExistence removes the caller's existence permission for objectID.
func (g *Graph) RevokeExistence(caller, objectID ID) error {
	if err := ValidateID(caller); err != nil {
		return err
	}
	if err := ValidateID(objectID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if set, ok := g.existence[caller]; ok && set[objectID] {
		delete(set, objectID)
		g.seq++
	}
	return nil
}

// GrantTraversal lets caller traverse links of type linkTypeID.
func (g *Graph) GrantTraversal(caller, linkTypeID ID) error {
	if err := ValidateID(caller); err != nil {
		return err
	}
	if err := ValidateID(linkTypeID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	set, ok := g.traversable[caller]
	if !ok {
		set = map[ID]bool{}
		g.traversable[caller] = set
	}
	if !set[linkTypeID] {
		set[linkTypeID] = true
		g.seq++
	}
	return nil
}

// RevokeTraversal removes the caller's traversal permission for linkTypeID.
func (g *Graph) RevokeTraversal(caller, linkTypeID ID) error {
	if err := ValidateID(caller); err != nil {
		return err
	}
	if err := ValidateID(linkTypeID); err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if set, ok := g.traversable[caller]; ok && set[linkTypeID] {
		delete(set, linkTypeID)
		g.seq++
	}
	return nil
}

// snapshot is the caller's fixed permission view for one query.
// Maps are deep copies taken while holding Graph.mu.
type snapshot struct {
	seq         uint64
	caller      ID
	existence   map[ID]bool
	traversable map[ID]bool
}

// takeSnapshot copies the caller's permission sets under the graph lock.
// The returned snapshot also records existence/absence of the endpoint
// identifiers at the same linearization point.
func (g *Graph) takeSnapshot(caller ID) snapshot {
	snap := snapshot{
		caller:      caller,
		existence:   map[ID]bool{},
		traversable: map[ID]bool{},
	}
	if set, ok := g.existence[caller]; ok {
		for k, v := range set {
			snap.existence[k] = v
		}
	}
	if set, ok := g.traversable[caller]; ok {
		for k, v := range set {
			snap.traversable[k] = v
		}
	}
	return snap
}
