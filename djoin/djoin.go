// Package djoin maintains materialized multiset inner join R(K,A)⋈S(K,B), emitting per-batch deltas ΔR⋈S_old + R_old⋈ΔS + ΔR⋈ΔS (PRE-batch state).
package djoin

import (
	"errors"
	"ontology/rel"
	"sync"
)

type Delta struct {
	K    int64
	A, B string
	Mult int
}

var ErrInvalidChange = errors.New("djoin: invalid change (Sign must be ±1, V non-empty)")
var ErrDeleteMissing = errors.New("djoin: batch rejected: deleting a row that does not exist")
var ErrViewLimit = errors.New("djoin: batch rejected: materialized view exceeds maxView distinct tuples")

type jKey struct {
	k    int64
	a, b string
}

// Engine is the incremental join; use New. probe (unexported, unread via API) counts rows reached via K lookups in the latest Feed.
type Engine struct {
	mu             sync.RWMutex
	r, s           *rel.Table
	mat            map[jKey]int
	maxView, probe int
}

func New(maxView int) *Engine {
	return &Engine{r: rel.New(), s: rel.New(), mat: map[jKey]int{}, maxView: maxView}
}

// agg nets one side.s rows per (K,V), rejects bad changes and deletes below
// zero vs the PRE-batch table, and drops net-zero pairs (they join no term).
func agg(rows []rel.Row, old *rel.Table) (map[int64]map[string]int, error) {
	m := map[int64]map[string]int{}
	for _, x := range rows {
		if (x.Sign != 1 && x.Sign != -1) || x.V == "" {
			return nil, ErrInvalidChange
		}
		if m[x.K] == nil {
			m[x.K] = map[string]int{}
		}
		m[x.K][x.V] += x.Sign
	}
	for k, sm := range m {
		for v := range sm {
			if sm[v] == 0 {
				delete(sm, v)
			} else if old.Get(k, v)+sm[v] < 0 {
				return nil, ErrDeleteMissing
			}
		}
		if len(sm) == 0 {
			delete(m, k)
		}
	}
	return m, nil
}

// term adds d⋈opp into o (sw=false: ΔR⋈opp; sw=true: opp⋈ΔS), counting opposing
// rows found through the K index into n (never a full-table scan).
func term(o map[jKey]int, d map[int64]map[string]int, opp func(int64) []rel.Entry, sw bool, n *int) {
	for k, dm := range d {
		es := opp(k)
		*n += len(es)
		for x, dx := range dm {
			for _, en := range es {
				a, b := x, en.V
				if sw {
					a, b = en.V, x
				}
				o[jKey{k, a, b}] += dx * en.Mult // signed product: (−1)×(−1)=+1
			}
		}
	}
}

// Feed validates and applies one batch, returning the sorted non-zero tuples of
// J(R_new,S_new)−J(R_old,S_old). Every check runs before the commit, so a
// rejection leaves R, S and the view untouched.
func (e *Engine) Feed(dR, dS []rel.Row) ([]Delta, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	nr, err := agg(dR, e.r)
	if err != nil {
		return nil, err
	}
	ns, err := agg(dS, e.s)
	if err != nil {
		return nil, err
	}
	t := map[jKey]int{}
	n := 0
	term(t, nr, e.s.Lookup, false, &n) // ΔR ⋈ S_old
	term(t, ns, e.r.Lookup, true, &n)  // R_old ⋈ ΔS
	for k, dr := range nr {            // ΔR ⋈ ΔS
		for a, x := range dr {
			for b, y := range ns[k] {
				t[jKey{k, a, b}] += x * y
			}
		}
	}
	out := make([]Delta, 0, len(t))
	distinct := len(e.mat)
	for g, d := range t {
		if d == 0 {
			continue
		}
		out = append(out, Delta{g.k, g.a, g.b, d})
		if e.mat[g] == 0 {
			distinct++
		} else if e.mat[g]+d == 0 {
			distinct--
		}
	}
	if distinct > e.maxView {
		return nil, ErrViewLimit
	}
	apply(e.r, nr) // commit: reached only after every check above passed
	apply(e.s, ns)
	for _, d := range out {
		g := jKey{d.K, d.A, d.B}
		if v := e.mat[g] + d.Mult; v == 0 {
			delete(e.mat, g) // zero-mult tuples are removed, never stored at 0
		} else {
			e.mat[g] = v
		}
	}
	e.probe = n
	sortD(out)
	return out, nil
}

// View returns a sorted snapshot of R ⋈ S after some complete batch; the read lock
// means a concurrent reader never sees a half-applied batch.
func (e *Engine) View() []Delta {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Delta, 0, len(e.mat))
	for t, m := range e.mat {
		out = append(out, Delta{t.k, t.a, t.b, m})
	}
	sortD(out)
	return out
}
