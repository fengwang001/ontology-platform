// Package api is the public facade over a cluster of LWW replicas.
package api

import (
	"errors"
	"maps"
	"math/rand"
	"slices"
	"sort"

	"ontology/cluster"
	"ontology/lww"
)

var ( // the four mutually distinct decidable error categories
	ErrParam     = cluster.ErrParam
	ErrElement   = lww.ErrElement
	ErrTimestamp = lww.ErrTimestamp
	ErrCapacity  = lww.ErrCapacity
)

type (
	Record = lww.Record
	Set    struct{ c *cluster.Cluster }
)

func New(n, maxElems int) (*Set, error) {
	if c, err := cluster.New(n, maxElems); err == nil {
		return &Set{c}, nil
	} else {
		return nil, err
	}
}

func (s *Set) Add(r int, e string, ts int64) error    { return s.op(r, e, ts, true) }
func (s *Set) Remove(r int, e string, ts int64) error { return s.op(r, e, ts, false) }
func (s *Set) op(r int, e string, ts int64, add bool) error {
	st, err := s.c.Replica(r)
	if err == nil && add {
		err = st.Add(e, ts)
	} else if err == nil {
		err = st.Remove(e, ts)
	}
	return err
}
func (s *Set) Merge(dst, src int) error { return s.c.Merge(dst, src) }
func (s *Set) SyncAll() error           { return s.c.SyncAll() }

func (s *Set) Contains(r int, e string) (bool, error) {
	st, err := s.c.Replica(r)
	return err == nil && st.Contains(e), err
}
func (s *Set) Records(r int) (map[string]Record, error) {
	if st, err := s.c.Replica(r); err != nil {
		return nil, err
	} else {
		return st.Snapshot(), nil
	}
}
func (s *Set) Elements(r int) ([]string, error) {
	recs, err := s.Records(r)
	out := []string{}
	for e, rec := range recs {
		if rec.Live() {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out, err
}

// SelfCheck verifies the four invariants on fresh internal instances.
func (s *Set) SelfCheck() error {
	st, _ := New(3, 1000) // 1+3: random ops + interleaved merges vs naive reference
	rng, naive := rand.New(rand.NewSource(1)), map[string]Record{}
	for i := 0; i < 300; i++ {
		r, e, ts := rng.Intn(3), string(rune('a'+rng.Intn(8))), int64(rng.Intn(20)+1)
		rec, add := naive[e], rng.Intn(2) == 0
		side := &rec.R
		if add {
			side = &rec.A
		}
		*side = max(*side, ts)
		if err := st.op(r, e, ts, add); err != nil {
			return err
		}
		naive[e] = rec
		if i%7 == 0 {
			st.Merge(rng.Intn(3), rng.Intn(3))
		}
	}
	st.SyncAll()
	for r := 0; r < 3; r++ {
		if m, _ := st.Records(r); !maps.Equal(m, naive) {
			return errors.New("selfcheck: naive mismatch")
		}
	}
	mk := func() *Set { // identical replays, used to check the merge laws
		t, _ := New(3, 100)
		g := rand.New(rand.NewSource(7))
		for i := 0; i < 30; i++ {
			t.op(i%3, string(rune('a'+g.Intn(6))), int64(g.Intn(30)+1), g.Intn(2) == 0)
		}
		return t
	}
	rec := func(t *Set, r int) map[string]Record { m, _ := t.Records(r); return m }
	seq := func(t *Set, ms ...int) *Set { // each m encodes Merge(m/10, m%10)
		for _, m := range ms {
			t.Merge(m/10, m%10)
		}
		return t
	}
	ok := maps.Equal(rec(seq(mk(), 1), 0), rec(seq(mk(), 10), 1)) // commutativity
	x := seq(mk(), 1, 2)
	ok = ok && maps.Equal(rec(x, 0), rec(seq(mk(), 12, 1), 0)) // associativity
	before := rec(x, 0)
	seq(x, 0, 2) // idempotency: x⊔x = x, (x⊔z)⊔z = x⊔z
	if !ok || !maps.Equal(before, rec(x, 0)) {
		return errors.New("selfcheck: merge laws")
	}
	f, _ := New(2, 2) // 4: four distinct errors, no trace left behind
	f.Add(0, "a", 1)
	f.Add(0, "b", 2)
	snap := rec(f, 0)
	for _, c := range [][2]error{
		{f.Add(2, "x", 1), ErrParam}, {f.Add(0, "", 1), ErrElement}, {f.Add(0, "c", 0), ErrTimestamp},
		{f.Add(0, "c", 3), ErrCapacity}, {f.Remove(0, "c", 5), ErrCapacity}, {f.Merge(0, 2), ErrParam},
	} {
		if !errors.Is(c[0], c[1]) {
			return errors.New("selfcheck: wrong error")
		}
	}
	if !maps.Equal(snap, rec(f, 0)) {
		return errors.New("selfcheck: rejected op left trace")
	}
	g, _ := New(3, 4) // rejected Merge leaves no trace; later merges stay complete
	for r, es := range [][]string{{"a", "b"}, {"c", "d", "e"}, {"c", "d"}} {
		for i, e := range es {
			g.Add(r, e, int64(i+1))
		}
	}
	snap0 := rec(g, 0)
	if rej := g.Merge(0, 1); !errors.Is(rej, ErrCapacity) || !maps.Equal(rec(g, 0), snap0) || g.Merge(0, 2) != nil {
		return errors.New("selfcheck: rejected merge left trace")
	}
	if el, _ := g.Elements(0); !slices.Equal(el, []string{"a", "b", "c", "d"}) {
		return errors.New("selfcheck: post-rejection merge incomplete")
	}
	return nil
}
