package refupdate

// CommitID is the unique, immutable identifier of a commit object.
type CommitID string

// Commit is an append-only commit object with zero or more parent commits.
type Commit struct {
	ID      CommitID
	Parents []CommitID
}

// Graph is an append-only commit graph. Commits are never removed or mutated,
// so a graph can be queried concurrently after the commits have been added.
type Graph struct {
	commits map[CommitID]*Commit

	// walkEpoch marks the current descendant search; commits are enqueued
	// at most once per search without allocating a fresh visited map.
	walkEpoch int
	marks     map[CommitID]int

	// CommitsVisited counts commit objects examined by all IsDescendant
	// calls; it backs the performance proof in tests.
	CommitsVisited int
}

// NewGraph returns an empty commit graph.
func NewGraph() *Graph {
	return &Graph{
		commits: make(map[CommitID]*Commit),
		marks:   make(map[CommitID]int),
	}
}

// AddCommit inserts a commit. Adding the same id again with identical parents
// is a no-op; conflicting parents panic (objects are immutable).
func (g *Graph) AddCommit(c Commit) {
	if existing, ok := g.commits[c.ID]; ok {
		if !sameParents(existing.Parents, c.Parents) {
			panic("refupdate: immutable commit " + string(c.ID) + " redefined with different parents")
		}
		return
	}
	parents := make([]CommitID, len(c.Parents))
	copy(parents, c.Parents)
	g.commits[c.ID] = &Commit{ID: c.ID, Parents: parents}
}

func sameParents(a, b []CommitID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Exists reports whether the commit object is present.
func (g *Graph) Exists(id CommitID) bool {
	_, ok := g.commits[id]
	return ok
}

// IsDescendant reports whether descendant equals ancestor or has ancestor in
// its ancestor set. The search walks only parent edges reachable from
// descendant: its cost is bounded by that ancestor cone and is independent of
// unrelated commits in the graph. Missing parents are treated as not-present
// (they model objects not yet added). Cycles terminate via per-query marks.
func (g *Graph) IsDescendant(descendant, ancestor CommitID) bool {
	if descendant == ancestor {
		return g.Exists(ancestor)
	}
	if !g.Exists(descendant) || !g.Exists(ancestor) {
		return false
	}
	g.walkEpoch++
	epoch := g.walkEpoch
	stack := []CommitID{descendant}
	g.marks[descendant] = epoch
	for len(stack) > 0 {
		n := len(stack) - 1
		cur := stack[n]
		stack = stack[:n]
		g.CommitsVisited++
		node := g.commits[cur]
		for _, p := range node.Parents {
			if p == ancestor {
				return true
			}
			if g.marks[p] == epoch {
				continue
			}
			g.marks[p] = epoch
			if _, ok := g.commits[p]; ok {
				stack = append(stack, p)
			}
		}
	}
	return false
}
