// Package dep ingests transactions in commit order and computes dependency depths (write/write only; read sets audit-only).
package dep

import (
	"errors"
	"fmt"
	"slices"
	"sort"
)

type Txn struct {
	Seq           int64
	Writes, Reads []string
}

var (
	ErrSeqGap      = errors.New("dep: seq must be previous accepted seq + 1")
	ErrEmptyWrites = errors.New("dep: write set must be non-empty")
	ErrEmptyKey    = errors.New("dep: read/write set contains an empty key")
)

type info struct {
	w     []string
	deps  []int64
	depth int
}

type Graph struct {
	txns   []info
	last   map[string]int64
	checks int // unexported: historical entries inspected by the most recent Add
}

func New() *Graph { return &Graph{last: map[string]int64{}} }

func dedup(in []string) []string {
	set, out := map[string]struct{}{}, make([]string, 0, len(in))
	for _, k := range in {
		if _, ok := set[k]; !ok {
			set[k], out = struct{}{}, append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Validate checks t against wantSeq without mutating any state.
func (g *Graph) Validate(t Txn, wantSeq int64) error {
	switch {
	case t.Seq != wantSeq:
		return ErrSeqGap
	case len(t.Writes) == 0:
		return ErrEmptyWrites
	case slices.Contains(t.Writes, "") || slices.Contains(t.Reads, ""):
		return ErrEmptyKey
	}
	return nil
}

// Add accepts one transaction after the accepted prefix.
func (g *Graph) Add(t Txn) error {
	if err := g.Validate(t, int64(len(g.txns)+1)); err != nil {
		return err
	}
	w := dedup(t.Writes)
	// Inspect only each key's last writer: on a writer chain each new writer
	// depends on its predecessor, whose depth dominates earlier writers.
	dset, top, ds := map[int64]struct{}{}, 0, []int64{}
	for _, k := range w {
		if s, ok := g.last[k]; ok {
			if _, seen := dset[s]; !seen {
				dset[s], ds = struct{}{}, append(ds, s)
			}
			if d := g.txns[s-1].depth; d > top {
				top = d
			}
		}
	}
	g.checks = len(dset)
	for _, k := range w {
		g.last[k] = t.Seq
	}
	g.txns = append(g.txns, info{w, ds, top + 1})
	return nil
}

func (g *Graph) Len() int { return len(g.txns) }

// Record returns deduplicated writes, dependencies and depth of seq.
func (g *Graph) Record(seq int64) (w []string, deps []int64, depth int, ok bool) {
	if seq >= 1 && int(seq) <= len(g.txns) {
		r := &g.txns[seq-1]
		return r.w, r.deps, r.depth, true
	}
	return nil, nil, 0, false
}

// VerifyDepths recomputes all depths with the O(n²) pairwise reference.
func (g *Graph) VerifyDepths() error {
	for i := range g.txns {
		want := 1
		for j := 0; j < i; j++ {
			if intersects(g.txns[j].w, g.txns[i].w) && g.txns[j].depth+1 > want {
				want = g.txns[j].depth + 1
			}
		}
		if want != g.txns[i].depth {
			return fmt.Errorf("dep: depth mismatch seq %d: want %d got %d", i+1, want, g.txns[i].depth)
		}
	}
	return nil
}

func intersects(a, b []string) bool {
	for _, k := range a {
		if slices.Contains(b, k) {
			return true
		}
	}
	return false
}

// CheckCostBound runs the m-scaling experiment, failing if the inspected
// count for a final two-key txn grows with m; the count is never exposed.
func CheckCostBound() error {
	for _, m := range []int{100, 1000, 10000} {
		for _, same := range []bool{false, true} {
			g := New()
			for i := 1; i <= m; i++ {
				k := fmt.Sprintf("k%d", i)
				if same {
					k = "x"
				}
				if err := g.Add(Txn{Seq: int64(i), Writes: []string{k}}); err != nil {
					return err
				}
			}
			fin := []string{fmt.Sprintf("k%d", m), "z"}
			if same {
				fin = []string{"x", "y"}
			}
			if err := g.Add(Txn{Seq: int64(m + 1), Writes: fin}); err != nil || g.checks > 2 {
				return errors.New("dep: cost check failed")
			}
		}
	}
	return nil
}
