// Package tup provides the hashable (Col1, Col2) tuple representation and the
// per-group tuple reference-count map. It has no dependencies on other
// packages in this module and holds no locks: the dd package serializes all
// access.
package tup

// T is the distinct-counting unit: the whole (Col1, Col2) tuple. Two rows
// with equal T values are the same distinct tuple even if they differ by
// RowID. The struct is comparable and usable as a map key.
type T struct {
	C1 string
	C2 int
}

// Group maps one tuple to the number of active rows currently holding it.
// distinct is the number of tuples whose reference count is strictly greater
// than zero, maintained incrementally on Add/Remove.
type Group struct {
	refs     map[T]int
	distinct int
}

// NewGroup returns an empty group.
func NewGroup() *Group {
	return &Group{refs: make(map[T]int)}
}

// Add records one more active row holding t. The distinct count increases
// exactly when the reference count flips 0 -> 1. Exactly one tuple is
// inspected per call.
func (g *Group) Add(t T) (flippedUp bool) {
	n := g.refs[t]
	g.refs[t] = n + 1
	if n == 0 {
		g.distinct++
		return true
	}
	return false
}

// Remove retracts one active row holding t. The distinct count decreases
// exactly when the reference count flips 1 -> 0; the zero entry is then
// deleted so the map only contains live tuples. Exactly one tuple is
// inspected per call.
func (g *Group) Remove(t T) (flippedDown bool) {
	n := g.refs[t]
	if n <= 1 {
		delete(g.refs, t)
		if n == 1 {
			g.distinct--
			return true
		}
		return false
	}
	g.refs[t] = n - 1
	return false
}

// Distinct returns the number of tuples with a positive reference count.
func (g *Group) Distinct() int { return g.distinct }

// Refs returns the current reference count of t (0 when absent). It exists
// for cross-checking against batch recomputation.
func (g *Group) Refs(t T) int { return g.refs[t] }

// Snapshot returns a copy of the live tuple -> reference-count map, for
// cross-checking the incrementally maintained state against a batch result.
func (g *Group) Snapshot() map[T]int {
	m := make(map[T]int, len(g.refs))
	for t, n := range g.refs {
		m[t] = n
	}
	return m
}

// Table maps a group key to its tuple reference-count group.
type Table struct {
	groups map[string]*Group
}

// NewTable returns an empty table.
func NewTable() *Table {
	return &Table{groups: make(map[string]*Group)}
}

// Get returns the group for key, creating an empty one when absent.
func (tb *Table) Get(key string) *Group {
	g := tb.groups[key]
	if g == nil {
		g = NewGroup()
		tb.groups[key] = g
	}
	return g
}

// Lookup returns the group for key and false when the key has never been
// seen, so callers never materialize empty groups for pure reads.
func (tb *Table) Lookup(key string) (*Group, bool) {
	g, ok := tb.groups[key]
	return g, ok
}

// Groups iterates all materialized groups, applying fn. The iteration order
// is unspecified; callers only aggregate.
func (tb *Table) Groups(fn func(key string, g *Group)) {
	for k, g := range tb.groups {
		fn(k, g)
	}
}
