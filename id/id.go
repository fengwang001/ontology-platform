// Package id defines the element index type and re-exports the sentinel
// errors used across the union-find packages.
package id

import "ontology/uf"

// Index identifies an instance participating in equivalence classes.
type Index int

// Sentinel errors; errors.Is distinguishes every failure mode.
var (
	ErrBadIndex     = uf.ErrBadIndex
	ErrNegativeSize = uf.ErrNegativeSize
)

// Equivalence maintains dynamic connected components of instance IDs.
type Equivalence struct {
	dsu *uf.DSU
}

// New creates an equivalence relation over n instance IDs.
func New(n int) (*Equivalence, error) {
	d, err := uf.New(n)
	if err != nil {
		return nil, err
	}
	return &Equivalence{dsu: d}, nil
}

// Root returns the component representative of x.
func (e *Equivalence) Root(x Index) (Index, error) {
	r, err := e.dsu.Find(int(x))
	return Index(r), err
}

// MarkEquivalent merges the equivalence classes of x and y; it returns true
// only when two distinct classes were merged.
func (e *Equivalence) MarkEquivalent(x, y Index) (bool, error) {
	return e.dsu.Union(int(x), int(y))
}

// Equivalent reports whether x and y are in the same equivalence class.
func (e *Equivalence) Equivalent(x, y Index) (bool, error) {
	return e.dsu.Connected(int(x), int(y))
}

// Classes returns the number of equivalence classes.
func (e *Equivalence) Classes() int { return e.dsu.Count() }

// LastFindHops exposes the hop counter of the most recent root lookup.
func (e *Equivalence) LastFindHops() int { return e.dsu.LastFindHops() }
