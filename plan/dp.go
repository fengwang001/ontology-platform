package plan

import (
	"errors"
	"fmt"
	"sort"

	"ontology/catalog"
	"ontology/cost"
	"ontology/stats"
)

// MaxTables is the hard cap on the number of tables in one optimization:
// the DP keeps one slot per subset, so 1<<MaxTables entries bound memory.
const MaxTables = 16

var (
	ErrTooManyTables = errors.New("too many tables for subset DP")
	ErrNoTables      = errors.New("no tables to optimize")
)

// Metrics reports how much work the DP did. Counters are per Optimize call.
type Metrics struct {
	subsets int64
	splits  int64
}

// Subsets is the number of subsets examined (<= 2^n).
func (m Metrics) Subsets() int64 { return m.subsets }

// Splits is the number of subset splits examined (<= 3^n).
func (m Metrics) Splits() int64 { return m.splits }

// Optimize picks the lowest-cost join order for the named tables. The
// catalog is only read, so concurrent calls are safe.
func Optimize(cat *catalog.Catalog, tables []string) (*Node, Metrics, error) {
	var m Metrics
	names := append([]string(nil), tables...)
	sort.Strings(names)
	if len(names) == 0 {
		return nil, m, ErrNoTables
	}
	if len(names) > MaxTables {
		return nil, m, fmt.Errorf("%w: %d > %d", ErrTooManyTables, len(names), MaxTables)
	}
	n := len(names)
	idx := make(map[string]int, n)
	rows := make([]int64, n)
	st := make([]*stats.Table, n)
	stale := make([]bool, n)
	for i, name := range names {
		info, ok := cat.Table(name)
		if !ok {
			return nil, m, fmt.Errorf("%w: %s", catalog.ErrUnknownTable, name)
		}
		idx[name] = i
		rows[i] = info.Rows
		st[i] = info.Stats
		stale[i] = info.Stale
	}
	preds := cat.Predicates()
	sort.Slice(preds, func(a, b int) bool { return preds[a].String() < preds[b].String() })
	adj := make([]uint64, n)
	for _, p := range preds {
		a, aok := idx[p.LTable]
		b, bok := idx[p.RTable]
		if !aok || !bok {
			continue
		}
		adj[a] |= 1 << b
		adj[b] |= 1 << a
	}
	join := func(l, r *Node, ps []catalog.Predicate, cartesian bool) *Node {
		sel := 1.0
		unrel := l.Unreliable || r.Unreliable
		for _, p := range ps {
			est := stats.EqSelectivity(st[idx[p.LTable]], p.LCol, st[idx[p.RTable]], p.RCol)
			sel *= est.Sel
			unrel = unrel || !est.Reliable
		}
		return &Node{
			Left: l, Right: r, Predicates: ps, Cartesian: cartesian,
			Card:       cost.Card(l.Card, r.Card, sel),
			Cost:       cost.Join(l.Card, r.Card, l.Cost, r.Cost),
			Unreliable: unrel, Stale: l.Stale || r.Stale,
		}
	}
	full := uint64(1)<<n - 1
	conn := make([]bool, full+1)
	for mask := uint64(1); mask <= full; mask++ {
		conn[mask] = len(components(mask, adj)) == 1
	}
	best := make([]*Node, full+1)
	for mask := uint64(1); mask <= full; mask++ {
		m.subsets++
		if mask&(mask-1) == 0 {
			i := bitsTrailing(mask)
			best[mask] = &Node{Table: names[i], Card: float64(rows[i]),
				Cost: cost.Scan(rows[i]), Stale: stale[i]}
			continue
		}
		if comps := components(mask, adj); len(comps) > 1 {
			acc := best[comps[0]]
			for _, c := range comps[1:] {
				acc = join(acc, best[c], nil, true)
			}
			best[mask] = acc
			continue
		}
		anchor := mask & (-mask)
		rest := mask ^ anchor
		for sub := rest; ; sub = (sub - 1) & rest {
			s1 := sub | anchor
			s2 := mask ^ s1
			if s2 != 0 {
				m.splits++
				if conn[s1] && conn[s2] {
					if cross := crossing(preds, idx, s1, s2); len(cross) > 0 {
						if cand := join(best[s1], best[s2], cross, false); better(cand, best[mask]) {
							best[mask] = cand
						}
					}
				}
			}
			if sub == 0 {
				break
			}
		}
	}
	return best[full], m, nil
}

// crossing returns the predicates with one endpoint in s1 and the other in
// s2, in deterministic (sorted) order.
func crossing(preds []catalog.Predicate, idx map[string]int, s1, s2 uint64) []catalog.Predicate {
	var out []catalog.Predicate
	for _, p := range preds {
		a, aok := idx[p.LTable]
		b, bok := idx[p.RTable]
		if !aok || !bok {
			continue
		}
		inA1, inB1 := s1&(1<<a) != 0, s1&(1<<b) != 0
		inA2, inB2 := s2&(1<<a) != 0, s2&(1<<b) != 0
		if (inA1 && inB2) || (inB1 && inA2) {
			out = append(out, p)
		}
	}
	return out
}

// components returns the connected components of mask in the predicate
// graph, ordered by each component's lowest table index.
func components(mask uint64, adj []uint64) []uint64 {
	var comps []uint64
	remaining := mask
	for remaining != 0 {
		seed := remaining & (-remaining)
		comp := seed
		for frontier := seed; frontier != 0; {
			next := uint64(0)
			for f := frontier; f != 0; f &= f - 1 {
				next |= adj[bitsTrailing(f&(-f))]
			}
			next &= mask &^ comp
			comp |= next
			frontier = next
		}
		comps = append(comps, comp)
		remaining &^= comp
	}
	return comps
}

func bitsTrailing(mask uint64) int {
	i := 0
	for mask&1 == 0 {
		mask >>= 1
		i++
	}
	return i
}
