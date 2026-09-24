// Package api is the public face of the semi-naive closure engine.
package api

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"ontology/semi"
)

// Sentinel errors for rejected operations; all mutually distinct.
var (
	ErrEmptyNode     = errors.New("ontology: empty node")
	ErrDuplicateEdge = errors.New("ontology: duplicate edge")
	ErrSelfLoop      = errors.New("ontology: self-loop edge")
	ErrSelfCheck     = errors.New("ontology: self-check failed")
)

// DB holds the edge set and the cached evaluation state.
type DB struct {
	mu    sync.Mutex
	edges [][2]string
	eng   *semi.Engine
}

// New validates the edge list atomically: any bad edge fails the whole call.
func New(edges [][2]string) (*DB, error) {
	d := &DB{}
	for _, e := range edges {
		if err := d.check(e); err != nil {
			return nil, err
		}
		d.edges = append(d.edges, e)
	}
	return d, nil
}

// check validates one edge against the current set; it mutates nothing.
func (d *DB) check(e [2]string) error {
	if e[0] == "" || e[1] == "" {
		return ErrEmptyNode
	}
	if e[0] == e[1] {
		return ErrSelfLoop
	}
	if slices.Contains(d.edges, e) {
		return ErrDuplicateEdge
	}
	return nil
}

// AddEdge adds one edge; a rejected edge changes nothing, DB stays usable.
func (d *DB) AddEdge(x, y string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.check([2]string{x, y}); err != nil {
		return err
	}
	d.edges = append(d.edges, [2]string{x, y})
	d.eng = nil // invalidate cached evaluation
	return nil
}

// Eval computes the closure once and returns a sorted copy of it.
func (d *DB) Eval() [][2]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.eng == nil {
		d.eng = semi.New(d.edges)
		d.eng.Eval()
	}
	return slices.Clone(d.eng.Path())
}

// Size returns the number of tuples in the evaluated closure.
func (d *DB) Size() int { return len(d.Eval()) }

// SelfCheck verifies the four invariants on built-in edge sets: naive-closure
// equality, empty final Delta, Delta-once, rejection leaves no trace.
func SelfCheck() error {
	fail := func(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(f, a...)) }
	for i, edges := range [][][2]string{
		{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"b", "d"}, {"d", "e"}, {"e", "b"}},
		{{"x", "y"}}, {},
	} {
		db, err := New(edges)
		if err != nil {
			return fail("set %d: %v", i, err)
		}
		if got := db.Eval(); !slices.Equal(got, naiveClosure(edges)) {
			return fail("set %d: mismatch with naive closure", i)
		}
		eng := semi.New(edges)
		eng.Eval()
		rounds := eng.Rounds()
		if len(rounds[len(rounds)-1].Delta) != 0 {
			return fail("set %d: no empty final delta", i)
		}
		seen := map[[2]string]bool{}
		for _, r := range rounds {
			for _, p := range r.Delta {
				if seen[p] {
					return fail("set %d: tuple re-entered delta", i)
				}
				seen[p] = true
			}
		}
		if len(seen) != db.Size() {
			return fail("set %d: delta union != path", i)
		}
	}
	db, _ := New([][2]string{{"a", "b"}})
	before := db.Eval()
	for _, bad := range [][2]string{{"", "x"}, {"a", "a"}, {"a", "b"}} {
		if db.AddEdge(bad[0], bad[1]) == nil || !slices.Equal(before, db.Eval()) {
			return fail("rejection left a trace")
		}
	}
	return nil
}

// naiveClosure returns reachability (paths of length >= 1) by BFS from every
// node, sorted; a node on a cycle reaches itself.
func naiveClosure(edges [][2]string) [][2]string {
	adj, nodes := map[string][]string{}, map[string]bool{}
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		nodes[e[0]], nodes[e[1]] = true, true
	}
	var out [][2]string
	for s := range nodes {
		seen := map[string]bool{}
		q := append([]string(nil), adj[s]...)
		for i := 0; i < len(q); i++ {
			if u := q[i]; !seen[u] {
				seen[u] = true
				out = append(out, [2]string{s, u})
				q = append(q, adj[u]...)
			}
		}
	}
	slices.SortFunc(out, func(x, y [2]string) int {
		return cmp.Or(strings.Compare(x[0], y[0]), strings.Compare(x[1], y[1]))
	})
	return out
}
