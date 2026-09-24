package djoin

import (
	"errors"
	"fmt"
	"reflect"
	"sort"

	"ontology/rel"
)

func sortD(o []Delta) {
	sort.Slice(o, func(i, j int) bool {
		a, b := o[i], o[j]
		return a.K < b.K || a.K == b.K && (a.A < b.A || a.A == b.A && a.B < b.B)
	})
}

func apply(t *rel.Table, d map[int64]map[string]int) {
	for k, m := range d {
		for v, z := range m {
			t.Add(k, v, z)
		}
	}
}

func rr(k int64, v string, s int) rel.Row { return rel.Row{K: k, V: v, Sign: s} }

type tab = map[int64]map[string]int

// naiveJoin recomputes R ⋈ S from scratch (oracle for invariants I1/I2).
func naiveJoin(r, s tab) map[jKey]int {
	j := map[jKey]int{}
	for k, rm := range r {
		for a, mr := range rm {
			for b, ms := range s[k] {
				j[jKey{k, a, b}] += mr * ms
			}
		}
	}
	return j
}

func applyNet(t tab, rows []rel.Row) {
	for _, x := range rows {
		if t[x.K] == nil {
			t[x.K] = map[string]int{}
		}
		t[x.K][x.V] += x.Sign
		if t[x.K][x.V] == 0 {
			delete(t[x.K], x.V)
		}
	}
}

func vm(v []Delta) map[jKey]int {
	m := map[jKey]int{}
	for _, d := range v {
		m[jKey{d.K, d.A, d.B}] = d.Mult
	}
	return m
}

func section3() (dr, ds [][]rel.Row, want [][]Delta) {
	dr = [][]rel.Row{{rr(1, "x", 1), rr(2, "y", 1)}, {rr(1, "z", 1), rr(2, "y", -1)}, {rr(1, "x", -1)}}
	ds = [][]rel.Row{{rr(1, "p", 1), rr(2, "q", 1)}, {rr(1, "r", 1), rr(2, "q", 1)}, {rr(1, "r", -1)}}
	want = [][]Delta{
		{{1, "x", "p", 1}, {2, "y", "q", 1}},
		{{1, "x", "r", 1}, {1, "z", "p", 1}, {1, "z", "r", 1}, {2, "y", "q", -1}},
		{{1, "x", "p", -1}, {1, "x", "r", -1}, {1, "z", "r", -1}},
	}
	return dr, ds, want
}

// SelfCheck replays the built-in batches on a scratch engine, verifying I1
// (view==recompute), I2 (delta==naive diff), I3 (prefix non-negativity) and
// I4 (rejection atomicity, three distinct sentinels), plus probe scaling.
func (e *Engine) SelfCheck() error {
	g := New(1 << 20)
	sdr, sds, want := section3()
	down := map[jKey]int{}
	nr, ns := tab{}, tab{}
	for i := range sdr {
		acc := naiveJoin(nr, ns)
		out, err := g.Feed(sdr[i], sds[i])
		if err != nil {
			return fmt.Errorf("section3 batch %d: %w", i+1, err)
		}
		if !reflect.DeepEqual(out, want[i]) {
			return fmt.Errorf("section3 batch %d: wrong deltas %v", i+1, out)
		}
		applyNet(nr, sdr[i])
		applyNet(ns, sds[i])
		for _, x := range out { // I2: applying the delta to the old join yields the new one
			h := jKey{x.K, x.A, x.B}
			acc[h] += x.Mult
			if acc[h] == 0 {
				delete(acc, h)
			}
			down[h] += x.Mult // I3: every downstream prefix multiplicity stays >= 0
			if down[h] < 0 {
				return errors.New("negative downstream prefix multiplicity")
			}
			if down[h] == 0 {
				delete(down, h)
			}
		}
		if !reflect.DeepEqual(acc, naiveJoin(nr, ns)) ||
			!reflect.DeepEqual(down, vm(g.View())) || !reflect.DeepEqual(vm(g.View()), naiveJoin(nr, ns)) {
			return fmt.Errorf("section3 batch %d: delta/view inconsistent", i+1)
		}
	}
	for _, c := range []struct {
		name   string
		dR, dS []rel.Row
		want   error
	}{
		{"bad-sign", []rel.Row{rr(1, "a", 0)}, nil, ErrInvalidChange},
		{"empty-v", []rel.Row{rr(1, "", 1)}, nil, ErrInvalidChange},
		{"delete-missing", []rel.Row{rr(1, "a", -1)}, nil, ErrDeleteMissing},
	} {
		h := New(4)
		if _, err := h.Feed(c.dR, c.dS); !errors.Is(err, c.want) || len(h.View()) != 0 {
			return fmt.Errorf("%s: err=%v dirty=%d", c.name, err, len(h.View()))
		}
	}
	small := New(1)
	_, err := small.Feed([]rel.Row{rr(1, "a", 1)}, []rel.Row{rr(1, "p", 1), rr(1, "q", 1)})
	if !errors.Is(err, ErrViewLimit) || len(small.View()) != 0 {
		return fmt.Errorf("view-limit: err=%v dirty=%d", err, len(small.View()))
	}
	for _, m := range []int{100, 1000, 10000} { // probe count must not grow with m
		h := New(1 << 21)
		dr, ds := make([]rel.Row, 0, m), make([]rel.Row, 0, m)
		for i := 0; i < m; i++ {
			dr = append(dr, rr(int64(i), "a", 1))
			ds = append(ds, rr(int64(i), "b", 1))
		}
		if _, err := h.Feed(dr, ds); err != nil {
			return err
		}
		if _, err := h.Feed([]rel.Row{rr(0, "a2", 1)}, nil); err != nil { // K=0 matches 1 row in S
			return err
		}
		if h.probe > 1 {
			return fmt.Errorf("probe=%d grows with m=%d", h.probe, m)
		}
	}
	return nil
}
