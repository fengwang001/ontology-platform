// Package semi implements semi-naive incremental evaluation of the single
// recursive rule path(x,y) :- edge(x,y); path(x,y) :- path(x,z), edge(z,y).
// It depends only on package rel.
package semi

import (
	"sort"

	"ontology/rel"
)

// Round describes one iteration of the fixpoint loop.
type Round struct {
	Delta      [][2]string // tuples entering Delta this round, sorted
	Candidates int         // join candidates produced this round
	Added      int         // candidates that were new (== len(Delta))
	PathSize   int         // size of path after this round
}

// Engine evaluates the transitive closure semi-naively. It is not safe for
// concurrent use while Eval is running; after Eval it is read-only.
type Engine struct {
	adj    map[string][]string // edge adjacency, each list sorted
	path   *rel.Set
	rounds []Round
	cand   int // total join candidates; unexported, never exposed
}

// New builds an Engine over the given edges (duplicates collapse).
func New(edges [][2]string) *Engine {
	e := &Engine{adj: map[string][]string{}, path: rel.New()}
	seen := rel.New()
	for _, eg := range edges {
		p := rel.Pair{X: eg[0], Y: eg[1]}
		if seen.Add(p) {
			e.adj[p.X] = append(e.adj[p.X], p.Y)
		}
	}
	for k := range e.adj {
		sort.Strings(e.adj[k])
	}
	return e
}

// Eval runs the semi-naive fixpoint iteration. It is idempotent.
//
// Round 0: Delta0 = edge, path = edge. Round k>=1 joins only Delta_{k-1}
// with edge; a candidate enters Delta_k iff it is not already in path
// (adding to path as we go gives set semantics: repeat derivations, from
// earlier rounds or within this one, are suppressed exactly once). The loop
// stops as soon as a Delta comes out empty; that final round is recorded.
func (e *Engine) Eval() {
	if e.rounds != nil {
		return
	}
	var delta []rel.Pair
	for x, ys := range e.adj {
		for _, y := range ys {
			p := rel.Pair{X: x, Y: y}
			e.path.Add(p)
			delta = append(delta, p)
		}
	}
	e.rounds = []Round{{Delta: tuples(delta), Added: len(delta), PathSize: e.path.Size()}}
	for len(delta) > 0 {
		var next []rel.Pair
		ncand := 0
		for _, d := range delta { // Delta-only propagation, never full path
			for _, y := range e.adj[d.Y] {
				ncand++
				if p := (rel.Pair{X: d.X, Y: y}); e.path.Add(p) {
					next = append(next, p)
				}
			}
		}
		e.cand += ncand
		e.rounds = append(e.rounds, Round{Delta: tuples(next), Candidates: ncand, Added: len(next), PathSize: e.path.Size()})
		delta = next
	}
}

// tuples converts pairs to [2]string; order follows ps.
func tuples(ps []rel.Pair) [][2]string {
	out := make([][2]string, len(ps))
	for i, p := range ps {
		out[i] = [2]string{p.X, p.Y}
	}
	return out
}

// less orders tuples by X, then Y.
func less(a, b [2]string) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	return a[1] < b[1]
}

// Path returns the computed closure in sorted order. Call Eval first.
func (e *Engine) Path() [][2]string { return tuples(e.path.Pairs()) }

// Rounds returns a copy of the per-round trace with each Delta sorted;
// the final empty round is included.
func (e *Engine) Rounds() []Round {
	out := make([]Round, len(e.rounds))
	for i, r := range e.rounds {
		d := append([][2]string(nil), r.Delta...)
		sort.Slice(d, func(a, b int) bool { return less(d[a], d[b]) })
		out[i] = Round{Delta: d, Candidates: r.Candidates, Added: r.Added, PathSize: r.PathSize}
	}
	return out
}
